// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.metrics

import dev.sard.server.agents.dispatch.MicrometerDispatchMetrics
import dev.sard.server.agents.results.MicrometerLogMetrics
import dev.sard.server.agents.results.MicrometerResultMetrics
import dev.sard.server.agents.results.ResultsConfiguration
import dev.sard.server.agents.stream.AgentSessionRegistry
import dev.sard.server.agents.stream.MicrometerStreamMetrics
import dev.sard.server.agents.stream.StreamFixtures
import dev.sard.server.notify.MicrometerNotifyMetrics
import dev.sard.server.pki.MovableClock
import dev.sard.server.retention.MicrometerRetentionMetrics
import dev.sard.server.retention.RetentionKind
import dev.sard.server.runs.RunFinished
import dev.sard.server.runs.RunKind
import dev.sard.server.runs.RunState
import dev.sard.server.runs.Trigger
import dev.sard.server.runs.WaitingSteps
import dev.sard.server.scheduler.FireKind
import dev.sard.server.scheduler.FireOutcome
import dev.sard.server.scheduler.MicrometerSchedulerMetrics
import io.micrometer.prometheusmetrics.PrometheusConfig
import io.micrometer.prometheusmetrics.PrometheusMeterRegistry
import java.lang.ref.Reference
import java.time.Duration
import java.time.Instant
import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNotNull
import kotlin.test.assertTrue

private val NOW: Instant = Instant.parse("2027-06-15T12:00:00Z")

/**
 * The metrics that existed before the exporter keep their names as Micrometer writes them for Prometheus (decision 4):
 * the series the specification lists, in the registry that renders them.
 */
class ExistingMetricsExpositionTest {
    private val registry = PrometheusMeterRegistry(PrometheusConfig.DEFAULT)

    private fun exposition() = Exposition(registry.scrape())

    @Test
    fun `Метрики без меток есть в экспозиции сразу после старта`() {
        MicrometerStreamMetrics(registry, AgentSessionRegistry(MovableClock(NOW), StreamFixtures.SETTINGS))
        MicrometerDispatchMetrics(registry)
        MicrometerLogMetrics(registry)
        MicrometerNotifyMetrics(registry)
        MicrometerSchedulerMetrics(registry)
        MicrometerRetentionMetrics(registry)

        val now = exposition()
        val unlabelled =
            listOf(
                "sard_agents_connected",
                "sard_agent_duplicate_sessions_total",
                "sard_agent_clock_skew_seconds_count",
                "sard_run_steps_queued",
                "sard_run_steps_dispatched",
                "sard_run_steps_redispatched_total",
                "sard_run_steps_dispatch_failed_total",
                "sard_run_step_logs_dropped_lines_total",
                "sard_run_step_logs_truncated_total",
                "sard_notify_pending",
                "sard_scheduler_lag_seconds",
            )
        for (name in unlabelled) assertNotNull(now.value(name), name)
        for (kind in RetentionKind.entries) {
            assertEquals(0.0, now.value("sard_retention_deleted_total", "kind" to kind.tag), kind.tag)
        }
        assertEquals(7, now.series("sard_retention_deleted_total").size)
    }

    @Test
    fun `Существующая метрика с меткой появляется после своего события`() {
        MicrometerResultMetrics(registry).recorded("succeeded")
        val finished =
            RunFinished(
                UUID(1, 1),
                UUID(2, 2),
                UUID(3, 3),
                UUID(4, 4),
                Trigger.MANUAL,
                RunKind.BACKUP,
                RunState.FAILED,
                "x",
                NOW,
                NOW,
            )
        ResultsConfiguration().runFinishedTrace(registry).runFinished(finished)
        val notify = MicrometerNotifyMetrics(registry)
        notify.sent("telegram")
        notify.retried("telegram")
        notify.undelivered("telegram", "expired")
        MicrometerSchedulerMetrics(registry).fired(FireKind.SCHEDULE, FireOutcome.RUN_CREATED)

        val now = exposition()
        assertEquals(1.0, now.value("sard_run_step_results_total", "outcome" to "succeeded"))
        assertEquals(1.0, now.value("sard_run_finished_total", "status" to "failed"))
        assertEquals(1.0, now.value("sard_notify_sent_total", "channel" to "telegram"))
        assertEquals(1.0, now.value("sard_notify_retries_total", "channel" to "telegram"))
        assertEquals(1.0, now.value("sard_notify_undelivered_total", "channel" to "telegram", "reason" to "expired"))
        assertEquals(1.0, now.value("sard_scheduler_fires_total", "kind" to "schedule", "outcome" to "run_created"))
    }

    @Test
    fun `Отставание планировщика и очередь шагов видны числами`() {
        // A gauge holds its source weakly: the adapters stay referenced until the exposition was read.
        val scheduler = MicrometerSchedulerMetrics(registry)
        val dispatch = MicrometerDispatchMetrics(registry)
        scheduler.lag(Duration.ofSeconds(45))
        dispatch.waiting(WaitingSteps(queued = 2, dispatched = 3))

        val now = exposition()
        assertEquals(45.0, now.value("sard_scheduler_lag_seconds"))
        assertEquals(2.0, now.value("sard_run_steps_queued"))
        assertEquals(3.0, now.value("sard_run_steps_dispatched"))
        assertTrue(now.families().all { it.startsWith("sard_") }, now.families().toString())
        Reference.reachabilityFence(scheduler)
        Reference.reachabilityFence(dispatch)
    }
}
