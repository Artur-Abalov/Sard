// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.notify

import dev.sard.server.TestcontainersConfiguration
import dev.sard.server.notify.telegram.BotReply
import dev.sard.server.notify.telegram.FakeBotApi
import dev.sard.server.notify.telegram.TEST_TOKEN
import dev.sard.server.persistence.TenantSessions
import dev.sard.server.persistence.UuidV7
import dev.sard.server.pki.MovableClock
import dev.sard.server.runs.RUNS_NOW
import dev.sard.server.runs.Runs
import dev.sard.server.runs.RunsTenant
import dev.sard.server.runs.RunsTestConfiguration
import dev.sard.server.runs.Sources
import dev.sard.server.verify.NoVerifierEpisodes
import dev.sard.server.verify.NoVerifierState
import dev.sard.server.verify.VerificationLoop
import dev.sard.server.verify.VerificationTestConfiguration
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import org.junit.jupiter.api.AfterAll
import org.junit.jupiter.api.Tag
import org.junit.jupiter.api.extension.ExtendWith
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.boot.test.system.CapturedOutput
import org.springframework.boot.test.system.OutputCaptureExtension
import org.springframework.context.annotation.Import
import org.springframework.jdbc.core.JdbcTemplate
import org.springframework.test.context.DynamicPropertyRegistry
import org.springframework.test.context.DynamicPropertySource
import java.security.SecureRandom
import java.sql.Timestamp
import java.time.Duration
import java.time.Instant
import java.util.UUID
import kotlin.test.AfterTest
import kotlin.test.BeforeTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

private const val CHAT = "-100778"
private const val CONSOLE = "https://sard.example.com"

/** What a seeded record carries besides its status; the level of a pass is 5 unless told otherwise. */
private data class Evidence(
    val level: Int? = null,
    val mismatches: String = "[]",
    val stages: String = "[]",
    val reason: String? = null,
    val restoreMillis: Long? = null,
    val jobReason: String = "cycle",
)

/** A source of the tenant, its snapshot and the step that made it. */
private class Watched(
    val id: UUID,
    val snapshot: UUID,
    val step: UUID,
)

/**
 * V6 (verification-notifications.feature): a failed restore verification, a source not verified for too long, a pass
 * after either and the «no verifier» state reach Telegram through the same queue as runs; successes stay silent.
 */
@ExtendWith(OutputCaptureExtension::class)
@Tag("mutflow-isolated")
@MutFlowTest(
    includeTargets =
        [
            VerificationDeliveries::class,
            VerificationNoticeFormatter::class,
            VerificationTelling::class,
            NoVerifierState::class,
        ],
)
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.NONE,
    properties = [
        "spring.grpc.server.port=0",
        "sard.notify.tick-interval=1h",
        "sard.verification.interval=1h",
        "sard.scheduler.interval=1h",
        "sard.freshness.interval=1h",
        "sard.verification.default-timezone=Europe/Berlin",
        "SARD_TELEGRAM_BOT_TOKEN=$TEST_TOKEN",
        "SARD_TELEGRAM_CHAT_ID=$CHAT",
        "SARD_NOTIFY_LANGUAGE=ru",
        "SARD_CONSOLE_PUBLIC_URL=$CONSOLE",
    ],
)
@Import(TestcontainersConfiguration::class, RunsTestConfiguration::class, VerificationTestConfiguration::class)
class VerificationNotificationsIntegrationTest(
    @Autowired private val service: NotificationService,
    @Autowired private val loop: NotificationLoop,
    @Autowired private val verification: VerificationLoop,
    @Autowired private val sources: Sources,
    @Autowired private val runs: Runs,
    @Autowired private val sessions: TenantSessions,
    @Autowired private val clock: MovableClock,
    @Autowired private val jdbc: JdbcTemplate,
) {
    private val tenant = RunsTenant(jdbc)
    private val verifier: UUID = UUID.randomUUID()

    companion object {
        private val fake = FakeBotApi()

        @JvmStatic
        @DynamicPropertySource
        fun botApi(registry: DynamicPropertyRegistry) {
            registry.add("sard.notify.telegram.api-url") { fake.uri.toString() }
        }

        @JvmStatic
        @AfterAll
        fun closeFake() {
            fake.close()
        }
    }

    @BeforeTest
    fun `a tenant with an agent and a verifier`() {
        loop.stop()
        clock.now = RUNS_NOW.plus(Duration.ofHours(2))
        fake.requests.clear()
        fake.fallback = FakeBotApi.ok()
        tenant.create()
        addVerifier()
    }

    @AfterTest
    fun `drop the tenant`() {
        val tables =
            listOf(
                "notification_deliveries",
                "no_verifier_episodes",
                "verification_stale_episodes",
                "restore_verifications",
                "verification_jobs",
                "snapshots",
            )
        for (table in tables) jdbc.update("delete from $table where tenant_id = ?", tenant.id)
        tenant.drop()
    }

    // --- fixtures

    private fun addVerifier() {
        jdbc.update(
            "insert into agents (id, tenant_id, hostname, registered_at, role) " +
                "values (?, ?, 'vrf1.example.com', ?, 'verifier')",
            verifier,
            tenant.id,
            Timestamp.from(RUNS_NOW),
        )
    }

    /** One tick of the notification queue, and the verification loop's. */
    private fun ticks(count: Int = 3) = repeat(count) { MutFlow.underTest { service.tick() } }

    private fun at(minutes: Long): Instant = RUNS_NOW.plus(Duration.ofMinutes(minutes))

    private fun watched(name: String = "db-main"): Watched {
        val source = sources.create(tenant.id, tenant.draft(name)).id
        val run = runs.start(tenant.id, source).id
        // The backup behind the snapshot is not what these tests are about: no message of a run.
        jdbc.update("update runs set trigger = 'verification' where id = ?", run)
        tenant.finish(run)
        val step = jdbc.queryForObject("select id from run_steps where run_id = ?", UUID::class.java, run)!!
        val snapshot = UUID.randomUUID()
        jdbc.update(
            "insert into snapshots (id, tenant_id, source_id, step_id, agent_id, repository_name, repository_id, " +
                "snapshot_id, total_bytes, added_bytes, created_at, partial) " +
                "values (?, ?, ?, ?, ?, 'main', ?, ?, 10, 1, ?, false)",
            snapshot,
            tenant.id,
            source,
            step,
            tenant.agentId,
            "c".repeat(64),
            "4f3a9c1e-$snapshot",
            Timestamp.from(RUNS_NOW),
        )
        return Watched(source, snapshot, step)
    }

    private fun record(
        source: Watched,
        status: String,
        minutes: Long,
        evidence: Evidence = Evidence(),
    ): UUID {
        val job = UUID.randomUUID()
        val record = UUID.randomUUID()
        val time = Timestamp.from(at(minutes))
        jdbc.update(
            "insert into verification_jobs (id, tenant_id, source_id, snapshot_ref, repository_id, plugin, reason, " +
                "status, created_at, deadline, closed_at) values (?, ?, ?, ?, ?, 'postgresql', ?, 'closed', ?, ?, ?)",
            job,
            tenant.id,
            source.id,
            source.snapshot,
            "c".repeat(64),
            evidence.jobReason,
            time,
            time,
            time,
        )
        jdbc.update(
            "insert into restore_verifications (id, tenant_id, job_id, source_id, snapshot_id, step_id, " +
                "verifier_agent_id, key_holders, status, level, reason, stages, mismatches, restore_duration_ms, " +
                "recovered, verified_at) values (?, ?, ?, ?, ?, ?, ?, 2, ?, ?, ?, cast(? as jsonb), " +
                "cast(? as jsonb), ?, false, ?)",
            record,
            tenant.id,
            job,
            source.id,
            source.snapshot,
            source.step,
            if (status == "no_verifier") null else verifier,
            status,
            evidence.level ?: if (status == "passed") 5 else 0,
            evidence.reason,
            evidence.stages,
            evidence.mismatches,
            evidence.restoreMillis,
            time,
        )
        return record
    }

    private fun failedAt(
        source: Watched,
        minutes: Long,
    ) = record(
        source,
        "failed",
        minutes,
        Evidence(
            level = 3,
            mismatches = """[{"name":"rows:public.orders","expected":"1204311","actual":"1204310"}]""",
            stages =
                """[{"level":4,"name":"fingerprint","state":"failed","exitCode":1,""" +
                    """"output":"QA-OUTPUT-MARKER","imageDigest":""}]""",
        ),
    )

    private fun episode(
        source: Watched,
        openedMinutes: Long,
        countedFrom: Instant? = at(0),
        closedBy: UUID? = null,
    ): UUID {
        val id = UUID.randomUUID()
        jdbc.update(
            "insert into verification_stale_episodes (id, tenant_id, source_id, counted_from, opened_at, closed_at, " +
                "closed_by_id, threshold_days, timezone) values (?, ?, ?, ?, ?, ?, ?, 2, 'Europe/Berlin')",
            id,
            tenant.id,
            source.id,
            Timestamp.from(countedFrom ?: at(0)),
            Timestamp.from(at(openedMinutes)),
            closedBy?.let { Timestamp.from(clock.now) },
            closedBy,
        )
        return id
    }

    private fun texts(): List<String> = List(fake.requests.size) { fake.shownText(it) }

    private fun statusOf(record: UUID): String? =
        jdbc
            .queryForList(
                "select status from notification_deliveries where verification_id = ?",
                String::class.java,
                record,
            ).singleOrNull()

    private fun deliveries(): Int =
        jdbc.queryForObject(
            "select count(*) from notification_deliveries where tenant_id = ?",
            Int::class.java,
            tenant.id,
        )!!

    // --- a failure

    @Test
    fun `the first failure of a source is told once, with the mismatch in words`() {
        val source = watched()
        val record = failedAt(source, 10)

        ticks()

        val expected =
            """
            ❌ Проверка восстановления провалилась: db-main
            Стадия: сравнение отпечатков
            Снимок: 4f3a9c1e
            Верификатор: vrf1.example.com
            Расхождения:
            — таблица public.orders: в источнике 1 204 311 строк, в копии 1 204 310
            Проверка: $record
            $CONSOLE/verifications/$record
            """.trimIndent()
        assertEquals(listOf(expected), texts())
        assertEquals("delivered", statusOf(record))
    }

    @Test
    fun `a second failure in a row is not told, nor is one after a verification that did not take place`() {
        val source = watched()
        record(source, "passed", 1)
        val first = failedAt(source, 10)
        ticks()
        val second = failedAt(source, 20)
        record(source, "cannot_verify", 25)
        val third = failedAt(source, 30)

        ticks()

        assertEquals(1, fake.requests.size)
        assertEquals(listOf("delivered", "skipped", "skipped"), listOf(first, second, third).map(::statusOf))
    }

    @Test
    fun `a failure after a pass is told again, and the pass between is told as a recovery`() {
        val source = watched()
        failedAt(source, 10)
        ticks()
        record(source, "passed", 20, Evidence(restoreMillis = 252_000))
        ticks()
        failedAt(source, 30)
        ticks()

        val first = texts().map { it.lines().first() }

        assertEquals(
            listOf(
                "❌ Проверка восстановления провалилась: db-main",
                "✅ Проверка восстановления снова прошла: db-main",
                "❌ Проверка восстановления провалилась: db-main",
            ),
            first,
        )
    }

    @Test
    fun `a verification that did not take place is not told by itself`() {
        for (status in listOf("cannot_verify", "infrastructure_failure", "no_verifier")) {
            val source = watched("db-$status")
            record(source, "passed", 1)
            val record = record(source, status, 10)

            ticks()

            assertEquals("skipped", statusOf(record), status)
        }
        assertEquals(0, fake.requests.size)
    }

    @Test
    fun `a pass without a failure or an episode before it is silent`() {
        val histories =
            listOf(
                emptyList(),
                listOf("passed"),
                listOf("cannot_verify"),
                listOf("failed", "passed"),
            )
        for ((index, history) in histories.withIndex()) {
            val source = watched("db-$index")
            history.forEachIndexed { step, status -> record(source, status, 1L + step) }
            fake.requests.clear()
            ticks()
            val pass = record(source, "passed", 100L + index)

            ticks()

            assertEquals("skipped", statusOf(pass), "history $history")
        }
    }

    @Test
    fun `a manual verification is told by the same rules`() {
        val source = watched()
        record(source, "passed", 1)
        record(source, "failed", 10, Evidence(level = 3, jobReason = "manual"))

        ticks()

        assertEquals(1, texts().count { it.startsWith("❌ Проверка восстановления провалилась") })
    }

    @Test
    fun `a verification run does not give the message of a run`() {
        val source = watched()
        val run = runs.start(tenant.id, source.id).id
        jdbc.update("update runs set trigger = 'verification' where id = ?", run)
        tenant.finish(run)

        ticks()

        assertEquals(0, fake.requests.size)
    }

    @Test
    fun `a source deleted before the send is still named`() {
        val source = watched()
        failedAt(source, 10)
        jdbc.update("update sources set deleted_at = ? where id = ?", Timestamp.from(clock.now), source.id)

        ticks()

        assertEquals("❌ Проверка восстановления провалилась: db-main", texts().single().lines().first())
    }

    @Test
    fun `the output of a stage, the config and the repository never reach the message`() {
        val source = watched()
        failedAt(source, 10)

        ticks()

        val message = texts().single()
        for (marker in listOf("QA-OUTPUT-MARKER", "pg-prod", "password", "secret")) {
            assertTrue(marker !in message, "$marker in $message")
        }
    }

    // --- a source not verified for too long

    @Test
    fun `a stale episode is told once, in the zone of the cycle, with the last outcome`() {
        val source = watched()
        record(source, "passed", 1)
        record(source, "no_verifier", 30)
        val episode = episode(source, openedMinutes = 60)

        ticks()

        val expected =
            """
            ⚠️ Восстановление не проверено дольше 2 дн.: db-main
            Последняя успешная проверка: 2026-09-30 12:01 Europe/Berlin
            Последний итог: нет подходящего верификатора
            $CONSOLE/sources/${source.id}
            """.trimIndent()
        assertEquals(listOf(expected), texts())
        assertEquals(
            "delivered",
            jdbc.queryForObject(
                "select status from notification_deliveries where stale_episode_id = ?",
                String::class.java,
                episode,
            ),
        )
    }

    @Test
    fun `an episode that stays open is told once however many cycles pass`() {
        val source = watched()
        episode(source, openedMinutes = 60)
        ticks()

        repeat(5) {
            verification.tick()
            ticks()
        }

        assertEquals(1, fake.requests.size)
    }

    @Test
    fun `a new episode after a pass is told again`() {
        val source = watched()
        val first = episode(source, openedMinutes = 60)
        ticks()
        val pass = record(source, "passed", 100)
        jdbc.update(
            "update verification_stale_episodes set closed_at = ?, closed_by_id = ? where id = ?",
            Timestamp.from(clock.now),
            pass,
            first,
        )
        ticks()
        episode(source, openedMinutes = 1000, countedFrom = at(100))
        ticks()

        val titles = texts().map { it.lines().first() }

        assertEquals(
            listOf(
                "⚠️ Восстановление не проверено дольше 2 дн.: db-main",
                "✅ Проверка восстановления снова прошла: db-main",
                "⚠️ Восстановление не проверено дольше 2 дн.: db-main",
            ),
            titles,
        )
        assertEquals("delivered", statusOf(pass))
    }

    @Test
    fun `an episode closed by turning verification off gives no message`() {
        val source = watched()
        episode(source, openedMinutes = 60)
        ticks()
        jdbc.update("update sources set verification_enabled = false where id = ?", source.id)

        verification.tick()
        ticks()

        assertEquals(1, fake.requests.size)
    }

    @Test
    fun `a stale episode is told before a failure made in the same tick`() {
        val source = watched()
        failedAt(source, 10)
        episode(source, openedMinutes = 60)

        ticks()

        assertEquals(2, fake.requests.size)
        assertTrue(texts().first().startsWith("⚠️ Восстановление не проверено"), texts().toString())
    }

    @Test
    fun `a series of failures does not hold back the stale episode`() {
        val source = watched()
        record(source, "passed", 1)
        failedAt(source, 10)
        failedAt(source, 20)
        failedAt(source, 30)
        ticks()
        episode(source, openedMinutes = 60)

        ticks()

        assertEquals(1, texts().count { it.startsWith("⚠️ Восстановление не проверено") })
    }

    @Test
    fun `each source that goes stale is told on its own`() {
        val names = listOf("a", "b", "c")
        for (name in names) episode(watched(name), openedMinutes = 60)

        ticks()

        assertEquals(names, texts().map { it.lines().first().substringAfterLast(": ") }.sorted())
    }

    // --- a pass after silence

    @Test
    fun `a pass that closed a stale episode is told as a recovery`() {
        val source = watched()
        record(source, "no_verifier", 10)
        record(source, "no_verifier", 20)
        val open = episode(source, openedMinutes = 30)
        ticks()
        val pass = record(source, "passed", 100, Evidence(level = 4))
        jdbc.update(
            "update verification_stale_episodes set closed_at = ?, closed_by_id = ? where id = ?",
            Timestamp.from(clock.now),
            pass,
            open,
        )

        ticks()

        assertEquals(2, fake.requests.size)
        assertEquals("✅ Проверка восстановления снова прошла: db-main", texts().last().lines().first())
        assertEquals("Уровень: 4 — данные совпали", texts().last().lines()[1])
    }

    @Test
    fun `a pass after failures that also closed an episode is one message`() {
        val source = watched()
        failedAt(source, 10)
        val open = episode(source, openedMinutes = 30)
        ticks()
        val before = fake.requests.size
        val pass = record(source, "passed", 100)
        jdbc.update(
            "update verification_stale_episodes set closed_at = ?, closed_by_id = ? where id = ?",
            Timestamp.from(clock.now),
            pass,
            open,
        )

        ticks()

        assertEquals(1, fake.requests.size - before)
        assertEquals(1, texts().count { "$pass" in it })
    }

    // --- no verifier

    private fun noVerifierSources(count: Int) = repeat(count) { watched("db-$it") }

    private fun revokeVerifier() {
        val now = Timestamp.from(clock.now)
        jdbc.update("update agents set revoked_at = ? where id = ?", now, verifier)
    }

    @Test
    fun `the state of no verifier is told once with the number of sources, not every cycle`() {
        noVerifierSources(3)
        revokeVerifier()
        verification.tick()

        ticks()
        repeat(5) {
            verification.tick()
            ticks()
        }

        val expected =
            """
            ⚠️ Проверка восстановления не работает: нет верификатора
            Источников с включённой проверкой: 3
            Выпустите токен с ролью «верификатор» и поставьте агента: инструкция на обзоре консоли
            $CONSOLE/
            """.trimIndent()
        assertEquals(listOf(expected), texts())
    }

    @Test
    fun `leaving the state is silent, entering it again is told again`() {
        noVerifierSources(1)
        revokeVerifier()
        verification.tick()
        ticks()
        val openEpisodes = {
            jdbc.queryForObject(
                "select count(*) from no_verifier_episodes where tenant_id = ? and closed_at is null",
                Int::class.java,
                tenant.id,
            )
        }
        assertEquals(1, openEpisodes())

        jdbc.update("update agents set revoked_at = null where id = ?", verifier)
        verification.tick()
        ticks()
        assertEquals(0, openEpisodes())
        assertEquals(1, fake.requests.size)

        revokeVerifier()
        verification.tick()
        ticks()

        assertEquals(2, fake.requests.size)
    }

    @Test
    fun `an installation without sources to verify is not in the state`() {
        revokeVerifier()

        verification.tick()
        ticks()

        assertEquals(0, fake.requests.size)
        assertEquals(0, tenant.count("no_verifier_episodes"))
    }

    @Test
    fun `a verifier that fitted no source does not make the state`() {
        val source = watched()
        record(source, "no_verifier", 10)

        verification.tick()
        ticks()

        assertEquals(0, tenant.count("no_verifier_episodes"))
        assertEquals(0, fake.requests.size)
    }

    // --- delivery as reliable as runs'

    @Test
    fun `a failure is held back while Telegram is unavailable and sent when it is back`() {
        val source = watched()
        failedAt(source, 10)
        fake.fallback = BotReply(503, "{}")
        ticks(1)

        fake.fallback = FakeBotApi.ok()
        clock.now = clock.now.plus(Duration.ofMinutes(10))
        ticks(1)

        assertEquals(2, fake.requests.size)
        assertEquals("❌ Проверка восстановления провалилась: db-main", texts().last().lines().first())
    }

    @Test
    fun `a message not sent within a day is logged as expired`(output: CapturedOutput) {
        val source = watched()
        val record = failedAt(source, 10)
        fake.fallback = BotReply(503, "{}")
        ticks(1)

        clock.now = clock.now.plus(Duration.ofHours(25))
        ticks(1)

        assertEquals("expired", statusOf(record))
        assertTrue(
            output.all.lines().any {
                " WARN " in it && "Notification of verification $record through telegram expired" in it
            },
        )
    }

    @Test
    fun `a refusal of Telegram to tell of a stale episode is logged with its reason`(output: CapturedOutput) {
        val source = watched()
        val episode = episode(source, openedMinutes = 60)
        fake.fallback = FakeBotApi.error(400, "Bad Request: chat not found")

        ticks(1)

        val line =
            output.all.lines().single {
                "Notification of stale episode $episode" in it && "through telegram failed" in it
            }
        assertTrue(" WARN " in line && "chat not found" in line, line)
    }

    @Test
    fun `a failure recorded before the server stopped is sent after it starts`() {
        val source = watched()
        failedAt(source, 10)
        val channel = CapturingChannel()
        val restarted = anotherServer(channel)

        repeat(3) { restarted.tick() }

        assertEquals(1, channel.sent.size)
    }

    @Test
    fun `two servers send a failure once`() {
        val source = watched()
        failedAt(source, 10)
        val channel = CapturingChannel()
        val other = anotherServer(channel)

        repeat(3) {
            service.tick()
            other.tick()
        }

        assertEquals(1, fake.requests.size + channel.sent.size)
    }

    private fun anotherServer(channel: CapturingChannel): NotificationService {
        val ids = { UuidV7(clock, SecureRandom()) }
        return NotificationService(
            Deliveries(sessions, ids(), { false }),
            OverdueDeliveries(sessions, ids()),
            CertificateDeliveries(sessions, ids()),
            VerificationDeliveries(sessions, ids()),
            listOf(channel),
            { null },
            { Message(Message.Text("alert")) },
            { null },
            { Message(Message.Text("certificate")) },
            VerificationNoticeFormatter(NoticeLanguage.RU, ConsoleUrl.parse(CONSOLE)),
            RetryPolicy(RetrySettings()),
            QueueSettings(batch = 10, lease = Duration.ofMinutes(5), ttl = Duration.ofHours(24)),
            clock,
        )
    }
}
