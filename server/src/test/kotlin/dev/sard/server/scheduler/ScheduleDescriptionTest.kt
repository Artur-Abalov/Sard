// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.scheduler

import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import kotlin.test.Test
import kotlin.test.assertEquals

/** F3b: a schedule in words, by the specification's table; whatever it does not hold is a custom schedule. */
@MutFlowTest
class ScheduleDescriptionTest {
    private fun words(
        cron: String,
        language: ScheduleLanguage,
    ) = MutFlow.underTest { ScheduleDescription.of(cron, language) }

    private fun check(
        cron: String,
        ru: String,
        en: String,
    ) {
        assertEquals(ru, words(cron, ScheduleLanguage.RU), "ru: $cron")
        assertEquals(en, words(cron, ScheduleLanguage.EN), "en: $cron")
    }

    @Test
    fun `daily and hourly schedules are described with their time`() {
        check("0 2 * * *", "Каждый день в 02:00", "Every day at 02:00")
        check("5 23 * * *", "Каждый день в 23:05", "Every day at 23:05")
        check("0 * * * *", "Каждый час в :00", "Every hour at :00")
        check("15 * * * *", "Каждый час в :15", "Every hour at :15")
    }

    @Test
    fun `weekdays are described as such, in both spellings`() {
        check("30 2 * * 1-5", "По будням в 02:30", "On weekdays at 02:30")
        check("30 2 * * MON-FRI", "По будням в 02:30", "On weekdays at 02:30")
    }

    @Test
    fun `a single day of the week is named, Sunday being 0 or 7`() {
        check("0 3 * * 0", "Каждое воскресенье в 03:00", "Every Sunday at 03:00")
        check("0 3 * * 7", "Каждое воскресенье в 03:00", "Every Sunday at 03:00")
        check("0 3 * * 1", "Каждый понедельник в 03:00", "Every Monday at 03:00")
        check("0 3 * * 2", "Каждый вторник в 03:00", "Every Tuesday at 03:00")
        check("0 3 * * 3", "Каждую среду в 03:00", "Every Wednesday at 03:00")
        check("0 3 * * 4", "Каждый четверг в 03:00", "Every Thursday at 03:00")
        check("0 3 * * fri", "Каждую пятницу в 03:00", "Every Friday at 03:00")
        check("0 3 * * 6", "Каждую субботу в 03:00", "Every Saturday at 03:00")
    }

    @Test
    fun `steps of minutes are described for the listed steps`() {
        check("* * * * *", "Каждую минуту", "Every minute")
        check("*/1 * * * *", "Каждую минуту", "Every minute")
        check("*/2 * * * *", "Каждые 2 минуты", "Every 2 minutes")
        check("*/5 * * * *", "Каждые 5 минут", "Every 5 minutes")
        check("*/15 * * * *", "Каждые 15 минут", "Every 15 minutes")
        check("*/30 * * * *", "Каждые 30 минут", "Every 30 minutes")
    }

    @Test
    fun `everything else is a custom schedule that shows its cron`() {
        val custom =
            listOf(
                "*/25 * * * *",
                "0 2 1-7 * 1",
                "0,30 2 * * *",
                "0 2 * 1 *",
                "00 2 * * *",
                "0 2 * * 1-4",
                "0 2 * * 8",
                "60 * * * *",
                "0 24 * * *",
            )
        for (cron in custom) check(cron, "Особое расписание: $cron", "Custom: $cron")
    }
}
