// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.metrics

import dev.sard.server.api.captureLogs
import dev.sard.server.extension.TenantResolver
import dev.sard.server.persistence.TenantSessions
import dev.sard.server.pki.CertificateAuthority
import dev.sard.server.pki.MovableClock
import dev.sard.server.prometheus.StateQueries
import dev.sard.server.retention.SceneTenant
import dev.sard.server.retention.T0
import dev.sard.server.runs.RUNS_NOW
import dev.sard.server.runs.RunAnnouncer
import dev.sard.server.selfbackup.S3_REPOSITORY
import dev.sard.server.selfbackup.SelfBackupTenant
import dev.sard.server.selfbackup.SelfBackups
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import io.micrometer.prometheusmetrics.PrometheusMeterRegistry
import org.junit.jupiter.api.Tag
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.jdbc.core.JdbcTemplate
import java.sql.Connection
import java.sql.Timestamp
import java.time.Duration
import java.time.Instant
import java.util.UUID
import kotlin.test.AfterTest
import kotlin.test.BeforeTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertFalse
import kotlin.test.assertNotNull
import kotlin.test.assertNull
import kotlin.test.assertTrue

private const val BACKUP = "sard_source_last_backup_success_timestamp_seconds"
private const val VERIFICATION = "sard_source_last_verification_success_timestamp_seconds"
private val DAY: Duration = Duration.ofDays(1)

private fun epoch(at: Instant) = at.epochSecond.toDouble()

/** What the state tick reads from PostgreSQL and puts into the exposition (H1 phase 3), across tenants. */
@Tag("mutflow-isolated")
@MutFlowTest(includeTargets = [StateQueries::class])
@MetricsServerTest
class MetricsStateIntegrationTest(
    @Autowired private val refresh: StateRefresh,
    @Autowired private val registry: PrometheusMeterRegistry,
    @Autowired private val clock: MovableClock,
    @Autowired private val jdbc: JdbcTemplate,
    @Autowired private val presence: FakePresence,
    @Autowired private val statements: StatementLog,
    @Autowired private val sessions: TenantSessions,
    @Autowired private val ca: CertificateAuthority,
    @Autowired private val selfBackups: SelfBackups,
    @Autowired private val announcer: RunAnnouncer,
) {
    private val scenes = mutableListOf<SceneTenant>()
    private val startedAt = clock.now

    @BeforeTest
    fun `the clock stands at T0 of the specification and the installation is empty`() {
        clock.now = T0
        presence.online = emptySet()
        scenes.forEach { it.drop() }
        scenes.clear()
    }

    @AfterTest
    fun `drop what the test made and move the clock back`() {
        scenes.forEach { it.drop() }
        scenes.clear()
        presence.online = emptySet()
        clock.now = startedAt
        refresh.tick()
    }

    private fun tenant() = SceneTenant(jdbc).also { scenes += it }

    private fun tick(at: Instant = clock.now) {
        clock.now = at
        MutFlow.underTest { refresh.tick() }
    }

    private fun exposition() = Exposition(registry.scrape())

    private fun SceneTenant.agent(
        role: String = "source",
        revoked: Boolean = false,
    ): UUID {
        val agent = UUID.randomUUID()
        jdbc.update(
            "insert into agents (id, tenant_id, hostname, registered_at, role, revoked_at) values (?, ?, ?, ?, ?, ?)",
            agent,
            id,
            "host-$agent",
            Timestamp.from(T0),
            role,
            if (revoked) Timestamp.from(T0) else null,
        )
        return agent
    }

    private fun SceneTenant.certificate(
        agent: UUID,
        notAfter: Instant,
        revoked: Boolean = false,
        predecessor: String? = null,
        superseded: Boolean = false,
    ): String {
        val serial = UUID.randomUUID().toString().replace("-", "")
        jdbc.update(
            """
            insert into agent_certificates (serial, tenant_id, agent_id, issued_at, not_after, revoked_at,
                                            predecessor_serial, superseded_at)
            values (?, ?, ?, ?, ?, ?, ?, ?)
            """.trimIndent(),
            serial,
            id,
            agent,
            Timestamp.from(notAfter.minus(Duration.ofDays(365))),
            Timestamp.from(notAfter),
            if (revoked) Timestamp.from(T0) else null,
            predecessor,
            if (superseded) Timestamp.from(T0) else null,
        )
        return serial
    }

    /** A source with a succeeded backup at [at] and a verification that [status] at [verifiedAt]. */
    private fun SceneTenant.verified(
        at: Instant,
        verifiedAt: Instant,
        status: String = "passed",
    ): UUID {
        val source = source()
        val backup = run(source, at, status = "succeeded")
        verification(source, snapshot(backup.steps.single(), source), verifiedAt, status)
        return source
    }

    private fun backupOf(source: UUID) = exposition().value(BACKUP, "source_id" to "$source")

    private fun verificationOf(source: UUID) = exposition().value(VERIFICATION, "source_id" to "$source")

    // ---- last success of a source -----------------------------------------------------------------------------

    @Test
    fun `Успешный бэкап виден в метрике источника после тика`() {
        val scene = tenant()
        val source = scene.source()
        scene.run(source, T0.minus(DAY), status = "succeeded")
        scene.run(source, T0, status = "succeeded")

        tick()

        assertEquals(epoch(T0), backupOf(source))
    }

    @Test
    fun `Между тиками метрика источника показывает значение прошлого тика`() {
        val scene = tenant()
        val source = scene.source()
        scene.run(source, T0.minus(DAY), status = "succeeded")
        tick(T0.minusSeconds(10))
        scene.run(source, T0, status = "succeeded")

        val between = backupOf(source)

        assertEquals(epoch(T0.minus(DAY)), between)
        tick(T0)
        assertEquals(epoch(T0), backupOf(source))
    }

    @Test
    fun `Неуспешный бэкап, restore и проверка не двигают время последнего бэкапа`() {
        val scene = tenant()
        val source = scene.source()
        scene.run(source, T0.minus(DAY), status = "succeeded")
        scene.run(source, T0, status = "failed")
        scene.run(source, T0, status = "cancelled")
        scene.run(source, T0, status = "succeeded", kind = "restore")
        scene.run(source, T0, status = "succeeded", trigger = "verification")

        tick()

        assertEquals(epoch(T0.minus(DAY)), backupOf(source))
    }

    @Test
    fun `Успешная проверка видна в метрике источника, неуспешная время не двигает`() {
        val scene = tenant()
        val source = scene.source()
        val backup = scene.run(source, T0.minus(Duration.ofDays(8)), status = "succeeded")
        val snapshot = scene.snapshot(backup.steps.single(), source)
        scene.verification(source, snapshot, T0.minus(Duration.ofDays(7)), "passed")
        for (status in listOf("failed", "cannot_verify", "infrastructure_failure", "no_verifier")) {
            scene.verification(source, snapshot, T0, status)
        }
        tick()
        assertEquals(epoch(T0.minus(Duration.ofDays(7))), verificationOf(source))

        scene.verification(source, snapshot, T0, "passed")
        tick()

        assertEquals(epoch(T0), verificationOf(source))
    }

    @Test
    fun `Живой источник без успехов отдаётся со значением 0`() {
        val source = tenant().source()

        tick()

        assertEquals(0.0, backupOf(source))
        assertEquals(0.0, verificationOf(source))
    }

    @Test
    fun `Удалённый источник исчезает из экспозиции на следующем тике`() {
        val scene = tenant()
        val source = scene.verified(T0, T0)
        tick()
        assertEquals(2, exposition().samples.count { it.labels["source_id"] == "$source" })

        jdbc.update("update sources set deleted_at = ? where id = ?", Timestamp.from(T0), source)
        tick()

        assertTrue(exposition().samples.none { it.labels["source_id"] == "$source" })
    }

    @Test
    fun `Источники всех тенантов видны в метриках`() {
        val a = tenant().verified(T0, T0)
        val b = tenant().verified(T0, T0)

        tick()

        assertNotNull(backupOf(a))
        assertNotNull(backupOf(b))
    }

    @Test
    fun `Переименование источника не создаёт новую серию`() {
        val source = tenant().verified(T0, T0)
        tick()
        val before = exposition().series(BACKUP).size

        jdbc.update("update sources set name = 'renamed' where id = ?", source)
        tick()

        assertEquals(1, exposition().series(BACKUP, "source_id" to "$source").size)
        assertEquals(before, exposition().series(BACKUP).size)
    }

    // ---- volume -----------------------------------------------------------------------------------------------

    private fun populate(
        scene: SceneTenant,
        sources: Int,
    ) {
        repeat(sources) { i ->
            val source = scene.source()
            val at = T0.minus(Duration.ofMinutes(i + 1L))
            val runs = scene.runs(source, 20, at, status = "succeeded")
            val snapshot = scene.snapshot(runs.first().steps.single(), source)
            scene.verifications(source, snapshot, 5, at.minusSeconds(30))
        }
    }

    private fun seriesWithoutSource(): Int {
        val sard = exposition().samples.filter { it.name.startsWith("sard_") }
        return sard.count { "source_id" !in it.labels }
    }

    @Test
    fun `Тысяча источников даёт ровно тысячу серий каждой метрики источника, значения - из базы`() {
        val scene = tenant()
        populate(scene, 1000)
        repeat(200) { index ->
            val source = scene.source(deleted = true)
            scene.run(source, T0.minusSeconds(index + 1L), status = "succeeded")
        }

        tick()

        val now = exposition()
        assertEquals(1000, now.series(BACKUP).size)
        assertEquals(1000, now.series(VERIFICATION).size)
        val backups =
            jdbc.queryForList(
                """
                select s.id, extract(epoch from max(r.finished_at)) as at
                from sources s join runs r on r.tenant_id = s.tenant_id and r.source_id = s.id
                where s.tenant_id = ? and s.deleted_at is null and r.status = 'succeeded' and r.kind = 'backup'
                  and r.trigger <> 'verification'
                group by s.id
                """.trimIndent(),
                scene.id,
            )
        assertEquals(1000, backups.size)
        for (row in backups) {
            val id = "${row["id"]}"
            assertEquals((row["at"] as Number).toDouble(), now.value(BACKUP, "source_id" to id), id)
        }
        val verifications =
            jdbc.queryForList(
                "select source_id, extract(epoch from max(verified_at)) as at from restore_verifications " +
                    "where tenant_id = ? and status = 'passed' group by source_id",
                scene.id,
            )
        for (row in verifications) {
            val value = now.value(VERIFICATION, "source_id" to "${row["source_id"]}")
            if (value != 0.0) assertEquals((row["at"] as Number).toDouble(), value, "${row["source_id"]}")
        }
    }

    @Test
    fun `Число запросов тика не зависит от числа источников`() {
        val scene = tenant()
        repeat(10) { scene.source() }
        val (_, few) = statements.hereDuring { tick() }

        repeat(990) { scene.source() }
        val (_, many) = statements.hereDuring { tick() }

        assertEquals(1000, exposition().series(BACKUP).size)
        assertTrue(few.isNotEmpty())
        assertEquals(few.size, many.size, "statements of the tick with 10 sources: $few")
    }

    @Test
    fun `Число серий метрик без source_id не зависит от объёма данных`() {
        tick()
        val empty = seriesWithoutSource()
        val scene = tenant()
        populate(scene, 100)
        repeat(50) { scene.agent() }
        val source = scene.source()
        scene.deliveries(source, 3000, T0.minus(Duration.ofHours(1)), "delivered")
        scene.deliveries(source, 2000, T0.minus(Duration.ofHours(2)), "failed")

        tick()

        assertEquals(empty, seriesWithoutSource())
    }

    // ---- agents, runs, queues -----------------------------------------------------------------------------

    @Test
    fun `Агент, ушедший офлайн, уменьшает число агентов на связи после тика`() {
        val scene = tenant()
        val first = scene.agent()
        val second = scene.agent()
        val verifier = scene.agent("verifier")
        presence.online = setOf(first, second, verifier)
        tick()
        assertEquals(2.0, exposition().value("sard_agents_online", "role" to "source"))

        presence.online = setOf(second, verifier)
        tick(T0.plusSeconds(200))

        val now = exposition()
        assertEquals(1.0, now.value("sard_agents_online", "role" to "source"))
        assertEquals(1.0, now.value("sard_agents_online", "role" to "verifier"))
        // The tenant's own agent is a source too: three sources and one verifier, as the database counts them.
        assertEquals(3.0, now.value("sard_agents", "role" to "source"))
        assertEquals(1.0, now.value("sard_agents", "role" to "verifier"))
    }

    @Test
    fun `Агент, вернувшийся на связь, увеличивает число агентов на связи после тика`() {
        val agent = tenant().agent()
        tick()
        assertEquals(0.0, exposition().value("sard_agents_online", "role" to "source"))

        presence.online = setOf(agent)
        tick()

        assertEquals(1.0, exposition().value("sard_agents_online", "role" to "source"))
    }

    @Test
    fun `Отозванный агент не считается, без агентов число каждой роли 0`() {
        tick()
        for (name in listOf("sard_agents", "sard_agents_online")) {
            assertEquals(listOf(0.0, 0.0), listOf("source", "verifier").map { exposition().value(name, "role" to it) })
        }
        val scene = tenant()
        scene.agent()
        scene.agent(revoked = true)

        tick()

        // The tenant's own agent and the one that is not revoked.
        assertEquals(2.0, exposition().value("sard_agents", "role" to "source"))
    }

    @Test
    fun `Активные запуски считаются по виду и статусу, завершённые не считаются`() {
        val scene = tenant()
        repeat(3) { scene.run(scene.source(), T0, status = "queued") }
        repeat(2) { scene.run(scene.source(), T0, status = "running") }
        scene.run(scene.source(), T0, status = "dispatched", kind = "restore")
        repeat(4) { scene.run(scene.source(), T0, status = "succeeded") }
        scene.run(scene.source(), T0, status = "running", trigger = "verification")

        tick()

        val series = exposition().series("sard_runs_active")
        assertEquals(9, series.size)

        fun active(
            kind: String,
            status: String,
        ) = series.single { it.labels == mapOf("kind" to kind, "status" to status) }.value
        assertEquals(3.0, active("backup", "queued"))
        assertEquals(2.0, active("backup", "running"))
        assertEquals(1.0, active("restore", "dispatched"))
        assertEquals(1.0, active("verify", "running"))
        assertEquals(5, series.count { it.value == 0.0 })
    }

    @Test
    fun `Очередь проверок восстановления считается по статусу`() {
        val scene = tenant()

        fun job(status: String) {
            val source = scene.source()
            val backup = scene.run(source, T0, status = "succeeded")
            val running =
                if (status == "running") scene.run(source, T0, status = "succeeded", trigger = "verification") else null
            scene.job(source, scene.snapshot(backup.steps.single(), source), status, running?.id)
        }
        repeat(4) { job("queued") }
        job("running")
        repeat(10) { job("closed") }

        tick()

        assertEquals(4.0, exposition().value("sard_verification_jobs", "status" to "queued"))
        assertEquals(1.0, exposition().value("sard_verification_jobs", "status" to "running"))
    }

    @Test
    fun `Неуспешные доставки за последние сутки считаются по статусу`() {
        val scene = tenant()
        val source = scene.source()
        val recent = T0.minus(Duration.ofHours(1))
        scene.deliveries(source, 2, recent, "failed")
        scene.deliveries(source, 3, recent, "expired")
        scene.deliveries(source, 7, T0.minus(Duration.ofHours(25)), "failed")
        scene.deliveries(source, 5, recent, "delivered")
        scene.deliveries(source, 1, recent, "pending")

        tick()

        assertEquals(2.0, exposition().value("sard_notify_deliveries_undelivered", "status" to "failed"))
        assertEquals(3.0, exposition().value("sard_notify_deliveries_undelivered", "status" to "expired"))
    }

    // ---- certificates -------------------------------------------------------------------------------------------

    private fun nextExpiry() = exposition().value("sard_agent_certificate_next_expiry_timestamp_seconds")

    @Test
    fun `Ближайшее истечение - минимум по текущим сертификатам агентов, всех тенантов`() {
        val a = tenant()
        val b = tenant()
        a.certificate(a.agent(), T0.plus(Duration.ofDays(300)))
        b.certificate(b.agent(), T0.plus(Duration.ofDays(20)))

        tick()

        assertEquals(epoch(T0.plus(Duration.ofDays(20))), nextExpiry())
    }

    @Test
    fun `Продление сертификата двигает ближайшее истечение`() {
        val scene = tenant()
        val a = scene.agent()
        val b = scene.agent()
        scene.certificate(a, T0.plus(Duration.ofDays(300)))
        val old = scene.certificate(b, T0.plus(Duration.ofDays(20)))
        tick()
        assertEquals(epoch(T0.plus(Duration.ofDays(20))), nextExpiry())

        jdbc.update("update agent_certificates set superseded_at = ? where serial = ?", Timestamp.from(T0), old)
        scene.certificate(b, T0.plus(Duration.ofDays(365)), predecessor = old)
        tick()

        assertEquals(epoch(T0.plus(Duration.ofDays(300))), nextExpiry())
    }

    @Test
    fun `Сертификат, не считающийся текущим, не входит в минимум`() {
        val scene = tenant()
        val a = scene.agent()
        scene.certificate(a, T0.plus(Duration.ofDays(300)))
        val soon = T0.plus(Duration.ofDays(5))
        val cases: List<(UUID) -> Unit> =
            listOf(
                { agent -> scene.certificate(agent, soon, revoked = true) },
                { agent ->
                    jdbc.update("update agents set revoked_at = ? where id = ?", Timestamp.from(T0), agent)
                    scene.certificate(agent, soon)
                },
                { agent ->
                    val old = scene.certificate(agent, soon, superseded = true)
                    scene.certificate(agent, T0.plus(Duration.ofDays(400)), predecessor = old)
                },
            )
        for (case in cases) {
            val b = scene.agent()
            case(b)

            tick()

            assertEquals(epoch(T0.plus(Duration.ofDays(300))), nextExpiry())
            jdbc.update("delete from agent_certificates where agent_id = ?", b)
        }
    }

    @Test
    fun `Уже истёкший сертификат входит в минимум, без сертификатов метрики ближайшего истечения нет`() {
        tick()
        assertNull(nextExpiry())
        val scene = tenant()
        scene.certificate(scene.agent(), T0.minus(Duration.ofDays(2)))
        scene.certificate(scene.agent(), T0.plus(Duration.ofDays(300)))

        tick()

        assertEquals(epoch(T0.minus(Duration.ofDays(2))), nextExpiry())
    }

    @Test
    fun `Срок CA - not_after сертификата CA сервера`() {
        tick()

        assertEquals(epoch(ca.notAfter()), exposition().value("sard_ca_certificate_expiry_timestamp_seconds"))
    }

    // ---- self-backup --------------------------------------------------------------------------------------------

    @Test
    fun `Не настроенный самобэкап - одна серия not_configured равна 1 и нет времени успеха`() {
        tick()

        val series = exposition().series("sard_self_backup_health")
        assertEquals(6, series.size)
        assertEquals(listOf("not_configured"), series.filter { it.value == 1.0 }.map { it.labels.getValue("health") })
        assertNull(exposition().value("sard_self_backup_last_success_timestamp_seconds"))
    }

    @Test
    fun `Состояние самобэкапа и время последнего успеха видны после тика`() {
        val self = SelfBackupTenant(jdbc, TenantResolver.DEFAULT_TENANT_ID).create()
        try {
            val scene = SceneTenant(jdbc, self.id to self.agentId)

            fun health(): Map<String, Double> =
                exposition().series("sard_self_backup_health").associate {
                    it.labels.getValue("health") to
                        it.value
                }
            tick()
            assertEquals(1.0, health().getValue("not_configured"))

            val bound = selfBackups.bind(self.id, S3_REPOSITORY, confirmLocalStorage = false)
            tick()
            assertEquals(1.0, health().getValue("ok"))
            assertEquals(List(5) { 0.0 }, health().filterKeys { it != "ok" }.values.toList())
            assertNull(exposition().value("sard_self_backup_last_success_timestamp_seconds"))

            scene.run(bound.sources[0].sourceId, T0.minus(Duration.ofHours(3)), status = "succeeded")
            tick()
            val lastSuccess = exposition().value("sard_self_backup_last_success_timestamp_seconds")
            assertEquals(epoch(T0.minus(Duration.ofHours(3))), lastSuccess)

            scene.run(bound.sources[0].sourceId, T0.minus(Duration.ofHours(1)), status = "failed")
            tick()
            assertEquals(1.0, health().getValue("failing"))
            assertEquals(0.0, health().getValue("ok"))
        } finally {
            self.drop()
        }
    }

    // ---- no writes, failures ------------------------------------------------------------------------------------

    @Test
    fun `Тик не пишет в базу, а чтение для метрик в транзакции только для чтения не даёт записать`() {
        val a = tenant()
        val b = tenant()
        a.verified(T0, T0)
        b.verified(T0, T0)
        a.agent()
        jdbc.queryForObject("select txid_current()", Long::class.java)
        val before = jdbc.queryForObject("select txid_current()", Long::class.java)!!

        tick()

        assertEquals(before + 1, jdbc.queryForObject("select txid_current()", Long::class.java))
        val failure =
            assertFailsWith<Exception> {
                sessions.inTenantReadOnly(a.id) { session ->
                    session.doWork { connection: Connection ->
                        val delete = "delete from sources where tenant_id = '${a.id}'"
                        connection.createStatement().use { it.execute(delete) }
                    }
                }
            }
        val chain = generateSequence<Throwable>(failure) { it.cause }.joinToString(" / ") { it.message.orEmpty() }
        assertTrue("read-only" in chain, chain)
    }

    @Test
    fun `Недоступная база оставляет прежние значения и признак устаревания, потом следующий тик обновляет`() {
        val scene = tenant()
        val source = scene.source()
        scene.run(source, T0.minus(DAY), status = "succeeded")
        tick(T0)
        val value = backupOf(source)
        val failures = exposition().value("sard_metrics_state_refresh_failures_total")!!
        jdbc.execute("alter table notification_deliveries rename to notification_deliveries_unavailable")
        val finishedRun = scene.run(scene.source(), T0, status = "failed")
        jdbc.update("update runs set finished_at = ? where id = ?", Timestamp.from(T0.plusSeconds(15)), finishedRun.id)
        val failed = {
            exposition().value("sard_run_duration_seconds_count", "kind" to "backup", "status" to "failed")!!
        }
        val failedBefore = failed()
        val lines =
            try {
                captureLogs { tick(T0.plusSeconds(60)) }.also {
                    announcer.announce(scene.id, finishedRun.steps.single())
                }
            } finally {
                jdbc.execute("alter table notification_deliveries_unavailable rename to notification_deliveries")
            }

        val now = exposition()
        assertEquals(value, backupOf(source))
        assertEquals(epoch(T0), now.value("sard_metrics_state_refresh_timestamp_seconds"))
        assertEquals(failedBefore + 1, failed(), "a failing tick does not stop the histogram")
        assertEquals(failures + 1, now.value("sard_metrics_state_refresh_failures_total"))
        val warning = lines.filter { it.startsWith("[${StateRefresh::class.java.name}]") }
        assertEquals(1, warning.size, lines.toString())
        val line = warning.single()
        assertFalse(line.contains("select", ignoreCase = true) || "$source" in line, line)
        assertFalse(lines.any { "notification_deliveries" in it && "$source" in it })

        scene.run(scene.source(), T0.plusSeconds(30), status = "succeeded")
        tick(T0.plusSeconds(120))
        assertEquals(epoch(T0.plusSeconds(120)), exposition().value("sard_metrics_state_refresh_timestamp_seconds"))
    }

    @Test
    fun `Первая метка времени обновления - время тика по часам сервера`() {
        tenant()

        tick(RUNS_NOW)

        assertEquals(epoch(RUNS_NOW), exposition().value("sard_metrics_state_refresh_timestamp_seconds"))
    }
}
