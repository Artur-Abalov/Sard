// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.metrics

import dev.sard.server.role.AgentRole
import dev.sard.server.runs.RunState
import dev.sard.server.selfbackup.SelfBackupHealth
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import io.micrometer.prometheusmetrics.PrometheusConfig
import io.micrometer.prometheusmetrics.PrometheusMeterRegistry
import java.time.Instant
import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNull
import kotlin.test.assertTrue

private val NOW: Instant = Instant.parse("2027-06-15T12:00:00Z")
private val SOURCE_A = UUID.fromString("00000000-0000-7000-8000-00000000000a")
private val SOURCE_B = UUID.fromString("00000000-0000-7000-8000-00000000000b")

private fun snapshot(
    sources: List<SourceSuccess> = emptyList(),
    agents: Map<AgentRole, Long> = emptyMap(),
    online: Map<AgentRole, Long> = emptyMap(),
    runs: Map<Pair<MetricRunKind, RunState>, Long> = emptyMap(),
    jobs: Map<String, Long> = emptyMap(),
    undelivered: Map<String, Long> = emptyMap(),
    certificate: Instant? = null,
    health: String = "not_configured",
    selfBackup: Instant? = null,
) = StateSnapshot(
    sources,
    agents,
    online,
    runs,
    jobs,
    undelivered,
    certificate,
    NOW.plusSeconds(1_000),
    health,
    selfBackup,
)

/** The gauges of the installation's state: what a tick puts into the exposition, and what it takes out again. */
@MutFlowTest(includeTargets = [StateGauges::class, MetricRunKind::class])
class StateGaugesTest {
    private val registry = PrometheusMeterRegistry(PrometheusConfig.DEFAULT)
    private val gauges = StateGauges(registry)

    private fun publish(snapshot: StateSnapshot) = MutFlow.underTest { gauges.publish(snapshot, NOW) }

    private fun exposition() = Exposition(registry.scrape())

    @Test
    fun `До первого тика метрик состояния нет`() {
        val names = exposition().samples.map { it.name }

        assertTrue(names.none { it.startsWith("sard_") }, names.toString())
    }

    @Test
    fun `Время успеха источника - Unix-время, без успеха 0, источники отдаются по source_id`() {
        val sources = listOf(SourceSuccess(SOURCE_A, NOW, NOW.minusSeconds(60)), SourceSuccess(SOURCE_B, null, null))
        publish(snapshot(sources = sources))

        val now = exposition()
        val backup = "sard_source_last_backup_success_timestamp_seconds"
        val verification = "sard_source_last_verification_success_timestamp_seconds"
        assertEquals(NOW.epochSecond.toDouble(), now.value(backup, "source_id" to "$SOURCE_A"))
        assertEquals(0.0, now.value(backup, "source_id" to "$SOURCE_B"))
        assertEquals(NOW.epochSecond - 60.0, now.value(verification, "source_id" to "$SOURCE_A"))
        assertEquals(0.0, now.value(verification, "source_id" to "$SOURCE_B"))
    }

    @Test
    fun `Источник, которого нет в следующем снимке, исчезает из экспозиции`() {
        publish(snapshot(sources = listOf(SourceSuccess(SOURCE_A, NOW, NOW), SourceSuccess(SOURCE_B, NOW, NOW))))
        publish(snapshot(sources = listOf(SourceSuccess(SOURCE_B, NOW, NOW))))

        val now = exposition()
        assertTrue(now.samples.none { it.labels["source_id"] == "$SOURCE_A" })
        assertEquals(2, now.samples.count { it.labels["source_id"] == "$SOURCE_B" })
    }

    @Test
    fun `Значение серии обновляется на месте, число серий не растёт`() {
        publish(snapshot(sources = listOf(SourceSuccess(SOURCE_A, NOW, null))))
        publish(snapshot(sources = listOf(SourceSuccess(SOURCE_A, NOW.plusSeconds(5), null))))

        val series = exposition().series("sard_source_last_backup_success_timestamp_seconds")
        assertEquals(listOf(NOW.epochSecond + 5.0), series.map { it.value })
    }

    @Test
    fun `Агенты по ролям отдаются все роли нулями, на связи - по ролям`() {
        publish(snapshot(agents = mapOf(AgentRole.SOURCE to 2), online = mapOf(AgentRole.SOURCE to 1)))

        val now = exposition()
        assertEquals(2.0, now.value("sard_agents", "role" to "source"))
        assertEquals(0.0, now.value("sard_agents", "role" to "verifier"))
        assertEquals(1.0, now.value("sard_agents_online", "role" to "source"))
        assertEquals(0.0, now.value("sard_agents_online", "role" to "verifier"))
    }

    @Test
    fun `Активные запуски - все девять серий вида и статуса, ненулевые на своих местах`() {
        publish(
            snapshot(
                runs =
                    mapOf(
                        (MetricRunKind.BACKUP to RunState.QUEUED) to 3L,
                        (MetricRunKind.BACKUP to RunState.RUNNING) to 2L,
                        (MetricRunKind.RESTORE to RunState.DISPATCHED) to 1L,
                    ),
            ),
        )

        val series = exposition().series("sard_runs_active")
        assertEquals(9, series.size)
        assertEquals(3.0, series.single { it.labels == mapOf("kind" to "backup", "status" to "queued") }.value)
        assertEquals(2.0, series.single { it.labels == mapOf("kind" to "backup", "status" to "running") }.value)
        assertEquals(1.0, series.single { it.labels == mapOf("kind" to "restore", "status" to "dispatched") }.value)
        assertEquals(6, series.count { it.value == 0.0 })
        assertEquals(setOf("backup", "restore", "verify"), series.map { it.labels.getValue("kind") }.toSet())
    }

    @Test
    fun `Очередь проверок и неуспешные доставки отдаются по статусу нулями и числами`() {
        publish(snapshot(jobs = mapOf("queued" to 4L), undelivered = mapOf("expired" to 3L)))

        val now = exposition()
        assertEquals(4.0, now.value("sard_verification_jobs", "status" to "queued"))
        assertEquals(0.0, now.value("sard_verification_jobs", "status" to "running"))
        assertEquals(0.0, now.value("sard_notify_deliveries_undelivered", "status" to "failed"))
        assertEquals(3.0, now.value("sard_notify_deliveries_undelivered", "status" to "expired"))
    }

    @Test
    fun `Срок сертификата агента есть, пока есть сертификат, срок CA есть всегда`() {
        publish(snapshot(certificate = NOW.plusSeconds(86_400)))
        val expiry = exposition().value("sard_agent_certificate_next_expiry_timestamp_seconds")
        assertEquals(NOW.epochSecond + 86_400.0, expiry)
        assertEquals(NOW.epochSecond + 1_000.0, exposition().value("sard_ca_certificate_expiry_timestamp_seconds"))

        publish(snapshot(certificate = null))

        assertFalse(exposition().has("sard_agent_certificate_next_expiry_timestamp_seconds"))
        assertTrue(exposition().has("sard_ca_certificate_expiry_timestamp_seconds"))
    }

    @Test
    fun `Состояние самобэкапа - шесть серий, ровно одна равна 1`() {
        // The labels are the states of the self-backup as the server decides them, in lower case.
        assertEquals(SelfBackupHealth.entries.map { it.name.lowercase() }, SELF_BACKUP_HEALTHS)
        for (health in SELF_BACKUP_HEALTHS) {
            publish(snapshot(health = health))

            val series = exposition().series("sard_self_backup_health")
            assertEquals(6, series.size)
            val ones = series.filter { it.value == 1.0 }
            assertEquals(listOf(health), ones.map { it.labels.getValue("health") })
            assertTrue(series.filter { it.value != 1.0 }.all { it.value == 0.0 })
        }
    }

    @Test
    fun `Время последнего успешного самобэкапа есть только когда был успех`() {
        publish(snapshot(selfBackup = NOW.minusSeconds(10_800)))
        assertEquals(NOW.epochSecond - 10_800.0, exposition().value("sard_self_backup_last_success_timestamp_seconds"))

        publish(snapshot(selfBackup = null))

        assertNull(exposition().value("sard_self_backup_last_success_timestamp_seconds"))
    }

    @Test
    fun `Время обновления - время тика`() {
        publish(snapshot())

        assertEquals(NOW.epochSecond.toDouble(), exposition().value("sard_metrics_state_refresh_timestamp_seconds"))
    }

    @Test
    fun `Метки метрик состояния - только из допустимого набора`() {
        publish(
            snapshot(
                sources = listOf(SourceSuccess(SOURCE_A, NOW, NOW)),
                agents = mapOf(AgentRole.SOURCE to 1),
                certificate = NOW,
                health = "ok",
                selfBackup = NOW,
            ),
        )

        val labels =
            exposition()
                .samples
                .filter { it.name.startsWith("sard_") }
                .flatMap { it.labels.keys }
                .toSet()
        assertEquals(setOf("source_id", "role", "kind", "status", "health"), labels)
    }
}
