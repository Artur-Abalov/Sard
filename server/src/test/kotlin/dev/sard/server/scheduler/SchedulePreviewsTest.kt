// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.scheduler

import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import java.time.Clock
import java.time.Instant
import java.time.ZoneId
import java.time.ZoneOffset
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith

/** F3b: what the console shows before a schedule is saved. */
@MutFlowTest
class SchedulePreviewsTest {
    private val t0 = Instant.parse("2026-10-09T12:00:00Z")

    private fun preview(
        cron: String,
        zone: String? = "UTC",
        now: Instant = t0,
        server: ZoneId = ZoneOffset.UTC,
        language: ScheduleLanguage = ScheduleLanguage.EN,
    ) = MutFlow.underTest { SchedulePreviews(Clock.fixed(now, ZoneOffset.UTC), server).of(cron, zone, language) }

    private fun instants(vararg values: String) = values.map(Instant::parse)

    @Test
    fun `the three nearest fires are in the schedule's zone and named with the cron as it will be saved`() {
        val result = preview("  0   2 * *  *  ", "Europe/Berlin")
        assertEquals("0 2 * * *", result.cron)
        assertEquals("Europe/Berlin", result.timezone)
        assertEquals(instants("2026-10-10T00:00:00Z", "2026-10-11T00:00:00Z", "2026-10-12T00:00:00Z"), result.nextFires)
        assertEquals("Every day at 02:00", result.description)
    }

    @Test
    fun `the nearest fires follow the switch to summer time`() {
        val now = Instant.parse("2027-03-26T12:00:00Z")
        val expected = instants("2027-03-27T01:30:00Z", "2027-03-28T01:00:00Z", "2027-03-29T00:30:00Z")
        assertEquals(expected, preview("30 2 * * *", "Europe/Berlin", now).nextFires)
    }

    @Test
    fun `a fire exactly now is not among the nearest`() {
        val now = Instant.parse("2026-10-10T00:00:00Z")
        val first = preview("0 2 * * *", "Europe/Berlin", now).nextFires.first()
        assertEquals(Instant.parse("2026-10-11T00:00:00Z"), first)
    }

    @Test
    fun `without a zone the server's zone is used and named`() {
        val result = preview("0 2 * * *", null, server = ZoneId.of("Europe/Moscow"))
        assertEquals("Europe/Moscow", result.timezone)
        assertEquals(Instant.parse("2026-10-09T23:00:00Z"), result.nextFires.first())
    }

    @Test
    fun `a server zone that is an offset and not an IANA region is replaced by UTC`() {
        assertEquals("UTC", preview("0 2 * * *", null, server = ZoneOffset.ofHours(3)).timezone)
    }

    @Test
    fun `the description is in the asked language`() {
        assertEquals("Каждый день в 02:00", preview("0 2 * * *", language = ScheduleLanguage.RU).description)
    }

    @Test
    fun `a wrong cron or zone is refused naming the field`() {
        assertEquals(ScheduleField.CRON, assertFailsWith<InvalidSchedule> { preview("0 2 * *") }.field)
        assertEquals(ScheduleField.TIMEZONE, assertFailsWith<InvalidSchedule> { preview("0 2 * * *", "") }.field)
        assertEquals(ScheduleField.TIMEZONE, assertFailsWith<InvalidSchedule> { preview("0 2 * * *", "+03:00") }.field)
    }

    @Test
    fun `a schedule is too frequent when two of the next hundred fires are closer than fifteen minutes`() {
        val frequent =
            listOf("* * * * *", "*/5 * * * *", "*/14 * * * *", "0,10 2 * * *", "0,10 2 1 1 *", "5,55 0,23 * * *")
        val rare = listOf("*/15 * * * *", "*/20 * * * *", "0 * * * *", "0 2 * * *")
        for (cron in frequent) assertEquals(true, preview(cron).tooFrequent, cron)
        for (cron in rare) assertEquals(false, preview(cron).tooFrequent, cron)
    }

    @Test
    fun `a schedule that fires rarely still previews three fires`() {
        val result = preview("0 0 29 2 *")
        assertEquals(3, result.nextFires.size)
    }
}
