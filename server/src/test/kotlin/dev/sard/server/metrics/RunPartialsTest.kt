// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.metrics

import dev.sard.server.runs.IncompleteReason
import dev.sard.server.runs.RunFinished
import dev.sard.server.runs.RunKind
import dev.sard.server.runs.RunState
import dev.sard.server.runs.Trigger
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import io.micrometer.prometheusmetrics.PrometheusConfig
import io.micrometer.prometheusmetrics.PrometheusMeterRegistry
import java.time.Instant
import java.util.UUID
import java.util.concurrent.CountDownLatch
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit
import kotlin.test.Test
import kotlin.test.assertEquals

private val T0: Instant = Instant.parse("2027-06-15T12:00:00Z")
private const val SERIES = "sard_run_partial_total"
private val REASONS = listOf("unreadable_files", "snapshot_set_incomplete", "unknown")

/** `sard.run.partial{reason}`: backups that failed after saving a snapshot, by why it is incomplete (НБ10). */
@MutFlowTest(includeTargets = [RunPartials::class])
class RunPartialsTest {
    private val registry = PrometheusMeterRegistry(PrometheusConfig.DEFAULT)
    private val partials = RunPartials(registry)

    private fun finished(
        status: RunState = RunState.FAILED,
        reason: IncompleteReason? = IncompleteReason.UNREADABLE_FILES,
    ) = RunFinished(
        UUID(1, 1),
        UUID.randomUUID(),
        UUID(2, 2),
        UUID(3, 3),
        Trigger.MANUAL,
        RunKind.BACKUP,
        status,
        null,
        T0,
        T0,
        reason,
    )

    private fun counts() =
        Exposition(registry.scrape()).let { now ->
            REASONS.map { now.value(SERIES, "reason" to it) }
        }

    @Test
    fun `Счётчик неполных бэкапов есть сразу со всеми причинами и нулями`() {
        assertEquals(listOf(0.0, 0.0, 0.0), counts())
        assertEquals(3, Exposition(registry.scrape()).series(SERIES).size)
    }

    @Test
    fun `Неполный бэкап увеличивает счётчик своей причины и только его`() {
        val expected =
            mapOf(
                IncompleteReason.UNREADABLE_FILES to listOf(1.0, 0.0, 0.0),
                IncompleteReason.SNAPSHOT_SET_INCOMPLETE to listOf(0.0, 1.0, 0.0),
                IncompleteReason.UNKNOWN to listOf(0.0, 0.0, 1.0),
            )
        for ((reason, counts) in expected) {
            val own = PrometheusMeterRegistry(PrometheusConfig.DEFAULT)
            val listener = RunPartials(own)

            MutFlow.underTest { listener.runFinished(finished(reason = reason)) }

            val now = Exposition(own.scrape())
            assertEquals(counts, REASONS.map { now.value(SERIES, "reason" to it) }, "$reason")
        }
    }

    @Test
    fun `Провал без снимка, успех и отмена после снимка счётчик не меняют`() {
        MutFlow.underTest { partials.runFinished(finished(reason = null)) }
        MutFlow.underTest { partials.runFinished(finished(RunState.SUCCEEDED, reason = null)) }
        val cancelled = finished(RunState.CANCELLED, IncompleteReason.SNAPSHOT_SET_INCOMPLETE)
        MutFlow.underTest { partials.runFinished(cancelled) }

        assertEquals(listOf(0.0, 0.0, 0.0), counts())
    }

    @Test
    fun `Одновременные неполные завершения учитываются все`() {
        val pool = Executors.newFixedThreadPool(20)
        val start = CountDownLatch(1)
        val done =
            List(200) { finished() }.map { event ->
                pool.submit {
                    start.await()
                    partials.runFinished(event)
                }
            }
        start.countDown()
        done.forEach { it.get(30, TimeUnit.SECONDS) }
        pool.shutdown()

        assertEquals(listOf(200.0, 0.0, 0.0), counts())
    }

    @Test
    fun `У серий единственная метка reason`() {
        MutFlow.underTest { partials.runFinished(finished()) }

        assertEquals(
            listOf(setOf("reason")),
            Exposition(registry.scrape()).series(SERIES).map { it.labels.keys }.distinct(),
        )
    }
}
