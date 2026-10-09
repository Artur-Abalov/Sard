// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.scheduler

/** The language a schedule is described in; the console passes its own (F3b, `lang`). */
enum class ScheduleLanguage {
    RU,
    EN,
}

private const val SUNDAY_AS_SEVEN = 7

/** One day of the week: its cron names and its wording per language. */
private class Day(
    val numbers: Set<Int>,
    val name: String,
    val ru: String,
    val en: String,
)

private val DAYS =
    listOf(
        Day(setOf(0, SUNDAY_AS_SEVEN), "SUN", "Каждое воскресенье", "Every Sunday"),
        Day(setOf(1), "MON", "Каждый понедельник", "Every Monday"),
        Day(setOf(2), "TUE", "Каждый вторник", "Every Tuesday"),
        Day(setOf(3), "WED", "Каждую среду", "Every Wednesday"),
        Day(setOf(4), "THU", "Каждый четверг", "Every Thursday"),
        Day(setOf(5), "FRI", "Каждую пятницу", "Every Friday"),
        Day(setOf(6), "SAT", "Каждую субботу", "Every Saturday"),
    )

/** The steps of minutes the table words, with the Russian noun that goes with each. */
private val MINUTE_STEPS = mapOf(2 to "минуты", 5 to "минут", 15 to "минут", 30 to "минут")

/**
 * A normalised cron in words (F3b, decision 12): the specification's table, 24-hour times on both
 * languages. What the table lacks is "custom" with the cron itself, so a wrong guess is never worded.
 */
object ScheduleDescription {
    private const val MINUTES_IN_HOUR = 60
    private const val HOURS_IN_DAY = 24
    private const val MINUTE = 0
    private const val HOUR = 1
    private const val DAY_OF_MONTH = 2
    private const val MONTH = 3
    private const val DAY_OF_WEEK = 4
    private val STEP = Regex("\\*/(\\d+)")
    private val EVERY_DAY = "Каждый день" to "Every day"
    private val WEEKDAYS = "По будням" to "On weekdays"

    fun of(
        cron: String,
        language: ScheduleLanguage,
    ): String {
        val words = known(cron.split(' '))
        return when {
            words == null && language == ScheduleLanguage.RU -> "Особое расписание: $cron"
            words == null -> "Custom: $cron"
            language == ScheduleLanguage.RU -> words.first
            else -> words.second
        }
    }

    /** (ru, en), or null if the cron is not in the table. */
    private fun known(fields: List<String>): Pair<String, String>? =
        when {
            fields[DAY_OF_MONTH] != "*" || fields[MONTH] != "*" -> null
            fields[HOUR] == "*" && fields[DAY_OF_WEEK] == "*" -> minutes(fields[MINUTE])
            else -> timed(fields[MINUTE], fields[HOUR], fields[DAY_OF_WEEK])
        }

    private fun minutes(minute: String): Pair<String, String>? {
        val hourly = number(minute, MINUTES_IN_HOUR)?.let { "Каждый час в :${two(it)}" to "Every hour at :${two(it)}" }
        return hourly ?: steps(minute)
    }

    private fun steps(minute: String): Pair<String, String>? {
        val step =
            if (minute == "*") {
                1
            } else {
                STEP
                    .matchEntire(minute)
                    ?.groupValues
                    ?.get(1)
                    ?.toInt()
            }
        return when (step) {
            null -> null
            1 -> "Каждую минуту" to "Every minute"
            else -> MINUTE_STEPS[step]?.let { "Каждые $step $it" to "Every $step minutes" }
        }
    }

    private fun timed(
        minute: String,
        hour: String,
        dayOfWeek: String,
    ): Pair<String, String>? {
        val m = number(minute, MINUTES_IN_HOUR)
        val h = number(hour, HOURS_IN_DAY)
        val days = days(dayOfWeek)
        if (m == null || h == null || days == null) return null
        val time = "${two(h)}:${two(m)}"
        return "${days.first} в $time" to "${days.second} at $time"
    }

    private fun days(dayOfWeek: String): Pair<String, String>? =
        when {
            dayOfWeek == "*" -> EVERY_DAY
            dayOfWeek == "1-5" || dayOfWeek.uppercase() == "MON-FRI" -> WEEKDAYS
            else -> weekday(dayOfWeek)
        }

    private fun weekday(dayOfWeek: String): Pair<String, String>? {
        val numeric = number(dayOfWeek, SUNDAY_AS_SEVEN + 1)
        return DAYS.singleOrNull { dayOfWeek.uppercase() == it.name || numeric in it.numbers }?.let { it.ru to it.en }
    }

    /** A canonical number below [limit] (no leading zero), else null. */
    private fun number(
        field: String,
        limit: Int,
    ): Int? = field.toIntOrNull()?.takeIf { it < limit && it.toString() == field }

    private fun two(value: Int) = value.toString().padStart(2, '0')
}
