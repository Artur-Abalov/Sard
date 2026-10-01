// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.runs

import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import java.time.Instant
import kotlin.test.Test
import kotlin.test.assertEquals

private val AT: Instant = Instant.parse("2026-09-30T10:00:00Z")
private val LIMITS = LogLimits(maxLineBytes = 10, maxStepBytes = 20)

private fun line(text: String) = LogLine(AT, "info", text)

/** What of a LogChunk the server keeps (S7a, answer 7): line length, step total, one truncation mark. */
@MutFlowTest
class LogBudgetTest {
    @Test
    fun `lines within both limits are kept as they are`() {
        val lines = listOf(line("one"), line("two"))

        val fit = MutFlow.underTest { LogBudget.fit(lines, used = 0, truncated = false, LIMITS) }

        assertEquals(LogFit(lines, bytes = 6, mark = false), fit)
    }

    @Test
    fun `a line longer than the limit is cut at the limit and marked`() {
        val fit = MutFlow.underTest { LogBudget.fit(listOf(line("0123456789A")), 0, false, LIMITS) }

        assertEquals(listOf(line("0123456789…[truncated]")), fit.kept)
        assertEquals(10, fit.bytes)
    }

    @Test
    fun `a line exactly at the limit is not cut`() {
        val fit = MutFlow.underTest { LogBudget.fit(listOf(line("0123456789")), 0, false, LIMITS) }

        assertEquals(listOf(line("0123456789")), fit.kept)
    }

    @Test
    fun `a cut never splits a character`() {
        // "ж" is two bytes in UTF-8: nine ASCII bytes and a half character would be ten.
        val fit = MutFlow.underTest { LogBudget.fit(listOf(line("012345678жж")), 0, false, LIMITS) }

        assertEquals(listOf(line("012345678…[truncated]")), fit.kept)
        assertEquals(9, fit.bytes)
    }

    @Test
    fun `NUL characters are removed, since PostgreSQL text refuses them`() {
        val fit = MutFlow.underTest { LogBudget.fit(listOf(line("a\u0000b")), 0, false, LIMITS) }

        assertEquals(listOf(line("ab")), fit.kept)
    }

    @Test
    fun `lines beyond the step's total are dropped after one mark`() {
        val lines = listOf(line("0123456789"), line("abcdefghij"), line("x"), line("y"))

        val fit = MutFlow.underTest { LogBudget.fit(lines, used = 5, truncated = false, LIMITS) }

        assertEquals(LogFit(lines.take(1), bytes = 10, mark = true), fit)
    }

    @Test
    fun `a line that ends exactly at the step's total is kept`() {
        val fit = MutFlow.underTest { LogBudget.fit(listOf(line("0123456789")), used = 10, truncated = false, LIMITS) }

        assertEquals(LogFit(listOf(line("0123456789")), bytes = 10, mark = false), fit)
    }

    @Test
    fun `a step already truncated keeps nothing and is not marked again`() {
        val fit = MutFlow.underTest { LogBudget.fit(listOf(line("x")), used = 20, truncated = true, LIMITS) }

        assertEquals(LogFit(emptyList(), bytes = 0, mark = false), fit)
    }

    @Test
    fun `the mark says where the log stopped`() {
        val mark = MutFlow.underTest { LogBudget.mark(LIMITS) }

        assertEquals(LogLine(null, "warn", "log truncated at 20 bytes; later lines of this step are dropped"), mark)
    }
}
