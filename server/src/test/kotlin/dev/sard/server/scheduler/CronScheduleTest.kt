// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.scheduler

import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import java.time.Instant
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith

/** Standard 5-field cron in an IANA zone; DST rules of F3a answer 5, day union of answer 6. */
@MutFlowTest
class CronScheduleTest {
    private fun fires(
        cron: String,
        zone: String,
        from: String,
        count: Int,
    ): List<Instant> =
        MutFlow.underTest {
            val schedule = CronSchedule.parse(cron, zone)
            generateSequence(schedule.nextAfter(Instant.parse(from))) { schedule.nextAfter(it) }.take(count).toList()
        }

    private fun instants(vararg values: String) = values.map(Instant::parse)

    @Test
    fun `every minute fires at the next whole minute, strictly after the given instant`() {
        val expected = instants("2026-10-08T10:01:00Z", "2026-10-08T10:02:00Z", "2026-10-08T10:03:00Z")
        assertEquals(expected, fires("* * * * *", "UTC", "2026-10-08T10:00:00Z", 3))
        assertEquals(instants("2026-10-08T10:01:00Z"), fires("* * * * *", "UTC", "2026-10-08T10:00:59.999Z", 1))
    }

    @Test
    fun `wall-clock time is read in the schedule's zone`() {
        val expected = instants("2026-12-01T08:00:00Z", "2026-12-02T08:00:00Z")
        assertEquals(expected, fires("0 9 * * *", "Europe/Berlin", "2026-11-30T09:00:00Z", 2))
    }

    @Test
    fun `restricted day of month and day of week fire on either, as in standard cron`() {
        // 2026-01-02 and 2026-01-09 are Fridays, 2026-01-13 is a Tuesday.
        val expected =
            instants("2026-01-02T00:00:00Z", "2026-01-09T00:00:00Z", "2026-01-13T00:00:00Z", "2026-01-16T00:00:00Z")
        assertEquals(expected, fires("0 0 13 * FRI", "UTC", "2026-01-01T00:00:00Z", 4))
    }

    @Test
    fun `a day field starting with a star restricts nothing, so the other day field alone decides`() {
        val fridays = instants("2026-01-02T00:00:00Z", "2026-01-09T00:00:00Z")
        assertEquals(fridays, fires("0 0 * * 5", "UTC", "2026-01-01T00:00:00Z", 2))
        val thirteenths = instants("2026-01-13T00:00:00Z", "2026-02-13T00:00:00Z")
        assertEquals(thirteenths, fires("0 0 13 * *", "UTC", "2026-01-01T00:00:00Z", 2))
        // Vixie cron: "*/2" in day of week starts with a star, so both day fields must match:
        // the 13th falling on Sunday, Tuesday, Thursday or Saturday (2026-01-13 Tue, 2026-06-13 Sat).
        val evenWeekdayThirteenths = instants("2026-01-13T00:00:00Z", "2026-06-13T00:00:00Z")
        assertEquals(evenWeekdayThirteenths, fires("0 0 13 * */2", "UTC", "2026-01-01T00:00:00Z", 2))
    }

    @Test
    fun `a starred step in day of week counts from Sunday as zero, as in standard cron`() {
        // From Sunday 2026-01-04 noon: Tue 6, Thu 8, Sat 10, Sun 11 (Spring alone would count from Monday).
        val expected =
            instants("2026-01-06T00:00:00Z", "2026-01-08T00:00:00Z", "2026-01-10T00:00:00Z", "2026-01-11T00:00:00Z")
        assertEquals(expected, fires("0 0 * * */2", "UTC", "2026-01-04T12:00:00Z", 4))
    }

    @Test
    fun `a fixed hour skipped by the spring-forward gap fires once, at the transition`() {
        // Berlin, 2026-03-29: 02:00 CET jumps to 03:00 CEST at 01:00Z.
        val expected = instants("2026-03-28T01:30:00Z", "2026-03-29T01:00:00Z", "2026-03-30T00:30:00Z")
        assertEquals(expected, fires("30 2 * * *", "Europe/Berlin", "2026-03-28T00:00:00Z", 3))
    }

    @Test
    fun `several fixed-hour times inside the gap collapse into the one transition fire`() {
        val expected = instants("2026-03-29T01:00:00Z", "2026-03-30T00:00:00Z")
        assertEquals(expected, fires("*/15 2 * * *", "Europe/Berlin", "2026-03-29T00:00:00Z", 2))
    }

    @Test
    fun `a fixed hour repeated by the fall-back overlap fires once, in its first occurrence`() {
        // New York, 2026-11-01: 01:00-01:59 EDT, then again 01:00-01:59 EST.
        val expected = instants("2026-10-31T05:30:00Z", "2026-11-01T05:30:00Z", "2026-11-02T06:30:00Z")
        assertEquals(expected, fires("30 1 * * *", "America/New_York", "2026-10-31T00:00:00Z", 3))
        // Asked from inside the second occurrence, the repeated time is not fired again.
        val fromSecond = fires("30 1 * * *", "America/New_York", "2026-11-01T06:10:00Z", 1)
        assertEquals(instants("2026-11-02T06:30:00Z"), fromSecond)
    }

    @Test
    fun `a starred hour follows real time through the overlap, firing in both occurrences`() {
        // Berlin, 2026-10-25: 03:00 CEST falls back to 02:00 CET at 01:00Z.
        val expected =
            instants(
                "2026-10-25T00:00:00Z",
                "2026-10-25T00:30:00Z",
                "2026-10-25T01:00:00Z",
                "2026-10-25T01:30:00Z",
                "2026-10-25T02:00:00Z",
            )
        assertEquals(expected, fires("*/30 * * * *", "Europe/Berlin", "2026-10-24T23:50:00Z", 5))
    }

    @Test
    fun `a starred hour follows real time through the gap, with no ghost fire`() {
        val expected = instants("2026-03-29T00:30:00Z", "2026-03-29T01:00:00Z", "2026-03-29T01:30:00Z")
        assertEquals(expected, fires("*/30 * * * *", "Europe/Berlin", "2026-03-29T00:20:00Z", 3))
    }

    @Test
    fun `month and weekday names are standard cron`() {
        val expected = instants("2027-01-01T00:00:00Z", "2027-01-04T00:00:00Z")
        assertEquals(expected, fires("0 0 * JAN mon-fri", "UTC", "2026-10-08T00:00:00Z", 2))
    }

    @Test
    fun `anything but five standard fields is refused, naming the cron field`() {
        val refused =
            listOf(
                "0 * * * * *",
                "* * * *",
                "",
                "@daily",
                "0 0 L * *",
                "0 0 15W * *",
                "0 0 * * 1#2",
                "0 0 ? * MON",
                "0 0 * * FRIDAY",
                "0 0 * MON *",
                "60 * * * *",
                "0 24 * * *",
                "0 0 30 2 *",
            )
        for (cron in refused) {
            val e = assertFailsWith<InvalidSchedule>(cron) { MutFlow.underTest { CronSchedule.parse(cron, "UTC") } }
            assertEquals(ScheduleField.CRON, e.field, cron)
        }
    }

    @Test
    fun `a zone that is not an IANA region is refused, naming the timezone field`() {
        for (zone in listOf("Mars/Olympus", "+03:00", "UTC+3", "", "CET+1")) {
            val e =
                assertFailsWith<InvalidSchedule>(zone) { MutFlow.underTest { CronSchedule.parse("* * * * *", zone) } }
            assertEquals(ScheduleField.TIMEZONE, e.field, zone)
        }
    }

    @Test
    fun `a parsed schedule keeps its text and zone`() {
        val schedule = MutFlow.underTest { CronSchedule.parse(" 0  9 * * 1 ", "Asia/Yerevan") }
        assertEquals("0 9 * * 1", schedule.cron)
        assertEquals("Asia/Yerevan", schedule.zone.id)
    }
}
