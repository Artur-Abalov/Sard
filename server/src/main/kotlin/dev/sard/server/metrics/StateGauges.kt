// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.metrics

import dev.sard.server.role.AgentRole
import dev.sard.server.runs.RunState
import io.micrometer.core.instrument.Gauge
import io.micrometer.core.instrument.MeterRegistry
import io.micrometer.core.instrument.Tag
import java.time.Instant
import java.util.concurrent.ConcurrentHashMap

private val ACTIVE_STATES = RunState.entries.filter { it.active }
private val JOB_STATUSES = listOf("queued", "running")
private val UNDELIVERED_STATUSES = listOf("failed", "expired")
private const val MILLIS_PER_SECOND = 1000.0

/** A series: a metric name and its labels, in the order they are written. */
private data class Series(
    val name: String,
    val labels: List<Pair<String, String>> = emptyList(),
)

/** What a gauge reads; written by a tick, read by a scrape. */
private class Cell(
    @Volatile var value: Double,
)

private fun Instant.seconds(): Double = toEpochMilli() / MILLIS_PER_SECOND

/**
 * The gauges of the installation's state (H1 phase 3). A tick hands over a [StateSnapshot]; the gauges keep its values
 * until the next one, so a scrape reads memory and never the database. Series come and go with the snapshot: a source
 * that is gone from it is gone from the exposition, a metric the snapshot has no value for (the certificate of no
 * agent) is not there. Before the first snapshot there is none of them.
 */
class StateGauges(
    private val meters: MeterRegistry,
) {
    private val cells = ConcurrentHashMap<Series, Pair<Cell, Gauge>>()

    @Synchronized
    fun publish(
        snapshot: StateSnapshot,
        at: Instant,
    ) {
        val wanted = seriesOf(snapshot, at)
        wanted.forEach { (series, value) -> set(series, value) }
        (cells.keys - wanted.keys).forEach { series -> cells.remove(series)?.let { meters.remove(it.second) } }
    }

    private fun set(
        series: Series,
        value: Double,
    ) {
        val existing = cells[series]
        if (existing != null) {
            existing.first.value = value
            return
        }
        val cell = Cell(value)
        val gauge =
            Gauge
                .builder(series.name, cell) { it.value }
                .tags(series.labels.map { Tag.of(it.first, it.second) })
                .register(meters)
        cells[series] = cell to gauge
    }

    private fun seriesOf(
        state: StateSnapshot,
        at: Instant,
    ): Map<Series, Double> =
        (
            listOf(Series(SardMetricNames.METRICS_STATE_REFRESH_TIMESTAMP) to at.seconds()) +
                sourceSeries(state) +
                agentSeries(state) +
                runSeries(state) +
                queueSeries(state) +
                certificateSeries(state) +
                selfBackupSeries(state)
        ).toMap()

    private fun sourceSeries(state: StateSnapshot): List<Pair<Series, Double>> =
        state.sources.flatMap { source ->
            val id = listOf("source_id" to source.sourceId.toString())
            listOf(
                Series(SardMetricNames.SOURCE_LAST_BACKUP_SUCCESS, id) to (source.lastBackup?.seconds() ?: 0.0),
                Series(SardMetricNames.SOURCE_LAST_VERIFICATION_SUCCESS, id) to
                    (source.lastVerification?.seconds() ?: 0.0),
            )
        }

    private fun agentSeries(state: StateSnapshot): List<Pair<Series, Double>> =
        AgentRole.entries.flatMap { role ->
            val labels = listOf("role" to role.stored)
            listOf(
                Series(SardMetricNames.AGENTS, labels) to (state.agents[role] ?: 0L).toDouble(),
                Series(SardMetricNames.AGENTS_ONLINE, labels) to (state.agentsOnline[role] ?: 0L).toDouble(),
            )
        }

    private fun runSeries(state: StateSnapshot): List<Pair<Series, Double>> =
        MetricRunKind.entries.flatMap { kind ->
            ACTIVE_STATES.map { status ->
                val labels = listOf("kind" to kind.tag, "status" to status.stored)
                Series(SardMetricNames.RUNS_ACTIVE, labels) to (state.runsActive[kind to status] ?: 0L).toDouble()
            }
        }

    private fun queueSeries(state: StateSnapshot): List<Pair<Series, Double>> =
        countSeries(SardMetricNames.VERIFICATION_JOBS, JOB_STATUSES, state.verificationJobs) +
            countSeries(SardMetricNames.NOTIFY_DELIVERIES_UNDELIVERED, UNDELIVERED_STATUSES, state.undelivered)

    /** One series `name{status}` for each of the [statuses], zero where [counts] has none. */
    private fun countSeries(
        name: String,
        statuses: List<String>,
        counts: Map<String, Long>,
    ): List<Pair<Series, Double>> =
        statuses.map { status ->
            val count = (counts[status] ?: 0L).toDouble()
            Series(name, listOf("status" to status)) to count
        }

    private fun certificateSeries(state: StateSnapshot): List<Pair<Series, Double>> =
        listOfNotNull(
            state.nextCertificateExpiry?.let { Series(SardMetricNames.AGENT_CERTIFICATE_NEXT_EXPIRY) to it.seconds() },
            Series(SardMetricNames.CA_CERTIFICATE_EXPIRY) to state.caExpiry.seconds(),
        )

    private fun selfBackupSeries(state: StateSnapshot): List<Pair<Series, Double>> =
        SELF_BACKUP_HEALTHS.map { health ->
            val labels = listOf("health" to health)
            Series(SardMetricNames.SELF_BACKUP_HEALTH, labels) to (if (health == state.selfBackupHealth) 1.0 else 0.0)
        } +
            listOfNotNull(
                state.selfBackupLastSuccess?.let { Series(SardMetricNames.SELF_BACKUP_LAST_SUCCESS) to it.seconds() },
            )
}
