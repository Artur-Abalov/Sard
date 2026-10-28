// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.verify

import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import java.time.Duration
import java.time.Instant
import java.time.LocalDate
import java.time.ZoneId
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith

private val NOW: Instant = Instant.parse("2026-10-26T10:00:00Z")
private val BERLIN = ZoneId.of("Europe/Berlin")
private val UTC = ZoneId.of("UTC")

private fun date(text: String) = LocalDate.parse(text)

/** V6: the period of a verification report; dates are whole days, both ends included, in the cycle's zone. */
@MutFlowTest
class ReportPeriodTest {
    private fun resolve(
        from: String?,
        to: String?,
        zone: ZoneId = UTC,
    ) = MutFlow.underTest { ReportPeriod.resolve(from?.let(::date), to?.let(::date), zone, NOW) }

    @Test
    fun `without dates the period is the last 30 days up to now`() {
        val period = resolve(null, null)

        assertEquals(NOW - Duration.ofDays(30), period.from)
        assertEquals(NOW, period.to)
    }

    @Test
    fun `dates mean whole days of the zone, the last one included`() {
        val period = resolve("2026-10-01", "2026-10-21", BERLIN)

        assertEquals(Instant.parse("2026-09-30T22:00:00Z"), period.from)
        assertEquals(Instant.parse("2026-10-21T22:00:00Z"), period.to)
    }

    @Test
    fun `a start alone runs to now, an end alone covers the 30 days before it`() {
        assertEquals(NOW, resolve("2026-10-01", null).to)
        val period = resolve(null, "2026-10-21")
        assertEquals(Instant.parse("2026-10-22T00:00:00Z"), period.to)
        assertEquals(Instant.parse("2026-09-22T00:00:00Z"), period.from)
    }

    @Test
    fun `an end before the start is refused at the end`() {
        val error = assertFailsWith<InvalidReportPeriod> { resolve("2026-10-26", "2026-10-01") }

        assertEquals("to", error.field)
    }

    @Test
    fun `one day is a period, a year and a day is the longest`() {
        assertEquals(Duration.ofDays(1), resolve("2026-10-26", "2026-10-26").let { Duration.between(it.from, it.to) })
        resolve("2025-10-25", "2026-10-26")

        val error = assertFailsWith<InvalidReportPeriod> { resolve("2025-10-24", "2026-10-26") }

        assertEquals("from", error.field)
    }

    @Test
    fun `a start alone is as long as the days to today`() {
        resolve("2025-10-25", null)

        assertEquals("from", assertFailsWith<InvalidReportPeriod> { resolve("2025-10-24", null) }.field)
    }
}
