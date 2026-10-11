// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import dev.sard.server.scheduler.InvalidSchedule
import dev.sard.server.scheduler.ScheduleField
import dev.sard.server.scheduler.SchedulePreviews
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import java.time.Clock
import java.time.Instant
import java.time.ZoneOffset
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith

/** F3b: the preview endpoint's language and its mapping to the contract, over the real previews. */
@MutFlowTest(includeTargets = [SchedulePreviewApiImpl::class])
class SchedulePreviewApiImplTest {
    private val now = Instant.parse("2026-10-09T12:00:00Z")
    private val api = SchedulePreviewApiImpl(SchedulePreviews(Clock.fixed(now, ZoneOffset.UTC), ZoneOffset.UTC))

    private fun preview(
        cron: String = "0 2 * * *",
        timezone: String? = "UTC",
        lang: String? = null,
    ) = MutFlow.underTest { api.preview(cron, timezone, lang) }

    @Test
    fun `the preview carries the cron, the zone, the fires and the warning as the previews computed them`() {
        val result = preview(" 0  2 * * * ", "Europe/Berlin")
        assertEquals("0 2 * * *", result.cron)
        assertEquals("Europe/Berlin", result.timezone)
        val fires = listOf("2026-10-10T00:00:00Z", "2026-10-11T00:00:00Z", "2026-10-12T00:00:00Z")
        assertEquals(fires.map(Instant::parse), result.nextFires)
        assertEquals(false, result.tooFrequent)
        assertEquals(true, preview("*/5 * * * *").tooFrequent)
    }

    @Test
    fun `the language is English by default and when asked, Russian when asked`() {
        assertEquals("Every day at 02:00", preview(lang = null).description)
        assertEquals("Every day at 02:00", preview(lang = "en").description)
        assertEquals("Каждый день в 02:00", preview(lang = "ru").description)
    }

    @Test
    fun `any other language is refused naming lang`() {
        for (lang in listOf("de", "", "RU", "En")) {
            val e = assertFailsWith<RequestInvalid>(lang) { preview(lang = lang) }
            assertEquals("lang", e.field, lang)
        }
    }

    @Test
    fun `a wrong cron or zone is the schedule's own refusal`() {
        assertEquals(ScheduleField.CRON, assertFailsWith<InvalidSchedule> { preview(cron = "") }.field)
        assertEquals(ScheduleField.TIMEZONE, assertFailsWith<InvalidSchedule> { preview(timezone = "Mars/X") }.field)
    }

    @Test
    fun `without a zone the server's is used`() {
        assertEquals("UTC", preview(timezone = null).timezone)
    }
}
