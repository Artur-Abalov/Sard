// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.runs

import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import java.time.Duration
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith

/** `sard.logs.*` and `sard.run.progress.*` (S7a, answers 6–8): defaults, and what is refused at start. */
@MutFlowTest
class RunsSettingsTest {
    @Test
    fun `the defaults are 8 KiB a line, 16 MiB a step, two months ahead checked daily, progress every 5 s`() {
        val logs = LogProperties()

        val values = MutFlow.underTest { listOf(logs.limits(), logs.monthsAhead(), logs.checkInterval()) }

        assertEquals(listOf(LogLimits(8192, 16L * 1024 * 1024), 2, Duration.ofDays(1)), values)
        assertEquals(Duration.ofSeconds(5), MutFlow.underTest { ProgressProperties().interval() })
    }

    @Test
    fun `the smallest accepted values pass`() {
        val logs = LogProperties(maxLineBytes = 1, maxStepBytes = 1, partitionsAheadMonths = 1)

        val values = MutFlow.underTest { listOf(logs.limits(), logs.monthsAhead()) }

        assertEquals(listOf(LogLimits(1, 1), 1), values)
        assertEquals(Duration.ofMillis(1), MutFlow.underTest { ProgressProperties(Duration.ofMillis(1)).interval() })
    }

    @Test
    fun `values that cannot work are refused with the setting's name`() {
        val refused =
            mapOf<() -> Any, String>(
                { LogProperties(maxLineBytes = 0).limits() } to "sard.logs.max-line-bytes must be at least 1",
                { LogProperties(maxLineBytes = 10, maxStepBytes = 9).limits() } to
                    "sard.logs.max-step-bytes must be at least sard.logs.max-line-bytes",
                { LogProperties(partitionsAheadMonths = 0).monthsAhead() } to
                    "sard.logs.partitions-ahead-months must be at least 1",
                { LogProperties(partitionsCheckInterval = Duration.ZERO).checkInterval() } to
                    "sard.logs.partitions-check-interval must be positive",
                { ProgressProperties(Duration.ZERO).interval() } to "sard.run.progress.write-interval must be positive",
            )
        for ((call, message) in refused) {
            val e = assertFailsWith<IllegalArgumentException> { MutFlow.underTest { call() } }
            assertEquals(message, e.message)
        }
    }
}
