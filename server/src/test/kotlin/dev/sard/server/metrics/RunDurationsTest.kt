// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.metrics

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
import kotlin.test.assertNull

private val T0: Instant = Instant.parse("2027-06-15T12:00:00Z")
private const val SERIES = "sard_run_duration_seconds"
private val KINDS = listOf("backup", "restore", "verify")
private val STATUSES = listOf("succeeded", "failed", "cancelled")

/** The histogram of run durations, written on the event of a run's end (H1 phase 3, МР8 and МР9). */
@MutFlowTest(includeTargets = [RunDurations::class])
class RunDurationsTest {
    private val registry = PrometheusMeterRegistry(PrometheusConfig.DEFAULT)
    private val durations = RunDurations(registry)

    private fun finished(
        kind: RunKind = RunKind.BACKUP,
        trigger: Trigger = Trigger.MANUAL,
        status: RunState = RunState.SUCCEEDED,
        startedAt: Instant? = T0,
        seconds: Long = 90,
    ) = RunFinished(
        UUID(1, 1),
        UUID.randomUUID(),
        UUID(2, 2),
        UUID(3, 3),
        trigger,
        kind,
        status,
        null,
        T0.plusSeconds(seconds),
        startedAt,
    )

    private fun exposition() = Exposition(registry.scrape())

    private fun count(
        kind: String,
        status: String,
    ) = exposition().value("${SERIES}_count", "kind" to kind, "status" to status)

    @Test
    fun `Завершение запуска добавляет наблюдение в гистограмму своего вида и итога`() {
        val cases =
            listOf(
                Triple("backup", "succeeded", finished(status = RunState.SUCCEEDED)),
                Triple("backup", "failed", finished(status = RunState.FAILED)),
                Triple("backup", "cancelled", finished(status = RunState.CANCELLED)),
                Triple("restore", "succeeded", finished(kind = RunKind.RESTORE, status = RunState.SUCCEEDED)),
                Triple("restore", "failed", finished(kind = RunKind.RESTORE, status = RunState.FAILED)),
                Triple("verify", "succeeded", finished(trigger = Trigger.VERIFICATION, status = RunState.SUCCEEDED)),
                Triple("verify", "failed", finished(trigger = Trigger.VERIFICATION, status = RunState.FAILED)),
            )
        for ((kind, status, event) in cases) {
            MutFlow.underTest { durations.runFinished(event) }
            val labels = arrayOf("kind" to kind, "status" to status)
            val now = exposition()
            assertEquals(1.0, now.value("${SERIES}_count", *labels), "$kind $status count")
            assertEquals(90.0, now.value("${SERIES}_sum", *labels), "$kind $status sum")
            assertEquals(1.0, now.value("${SERIES}_bucket", *labels, "le" to "300.0"), "$kind $status le 300")
            assertEquals(0.0, now.value("${SERIES}_bucket", *labels, "le" to "60.0"), "$kind $status le 60")
        }
    }

    @Test
    fun `Корзины гистограммы - 60 300 900 1800 3600 7200 14400 28800 86400 секунд`() {
        MutFlow.underTest { durations.runFinished(finished(seconds = 3)) }

        val bounds =
            exposition()
                .series("${SERIES}_bucket", "kind" to "backup", "status" to "succeeded")
                .map { it.labels.getValue("le") }
        assertEquals(
            listOf("60.0", "300.0", "900.0", "1800.0", "3600.0", "7200.0", "14400.0", "28800.0", "86400.0", "+Inf"),
            bounds,
        )
    }

    @Test
    fun `Запуск, отменённый до начала, не попадает в гистограмму`() {
        MutFlow.underTest { durations.runFinished(finished(status = RunState.CANCELLED, startedAt = null)) }

        val counts = KINDS.flatMap { k -> STATUSES.map { s -> count(k, s) } }
        assertEquals(List(9) { 0.0 }, counts)
    }

    @Test
    fun `Запуск по проверке восстановления считается видом verify`() {
        val verification = finished(kind = RunKind.RESTORE, trigger = Trigger.VERIFICATION)
        MutFlow.underTest { durations.runFinished(verification) }

        assertEquals(1.0, count("verify", "succeeded"))
        assertEquals(0.0, count("restore", "succeeded"))
    }

    @Test
    fun `Метки гистограммы - все комбинации видов и итогов с нулями`() {
        val counts = KINDS.flatMap { k -> STATUSES.map { s -> count(k, s) } }

        assertEquals(List(9) { 0.0 }, counts)
    }

    @Test
    fun `Одновременные завершения учитываются все`() {
        val pool = Executors.newFixedThreadPool(20)
        val start = CountDownLatch(1)
        val events = List(200) { finished() }
        val done =
            events.map { event ->
                pool.submit {
                    start.await()
                    durations.runFinished(event)
                }
            }
        start.countDown()
        done.forEach { it.get(30, TimeUnit.SECONDS) }
        pool.shutdown()

        assertEquals(200.0, count("backup", "succeeded"))
        assertNull(exposition().value("${SERIES}_count", "kind" to "restore", "status" to "bogus"))
    }
}
