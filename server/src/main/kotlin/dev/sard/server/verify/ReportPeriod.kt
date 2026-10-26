// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.verify

import java.time.Duration
import java.time.Instant
import java.time.LocalDate
import java.time.ZoneId
import java.time.temporal.ChronoUnit

/** A report period that cannot be: 422 at the field [field] (`from` or `to`). */
class InvalidReportPeriod(
    val field: String,
    message: String,
) : RuntimeException(message)

/** The period of a verification report: [from] included, [to] excluded. */
data class ReportPeriod(
    val from: Instant,
    val to: Instant,
) {
    companion object {
        private val DEFAULT_LENGTH: Duration = Duration.ofDays(30)
        private const val LONGEST_DAYS = 366L

        /**
         * Dates are whole days of [zone] (the cycle's), both ends included. Without any the period is the last 30 days
         * up to [now]; a start alone runs to now, an end alone covers the 30 days before it.
         */
        fun resolve(
            from: LocalDate?,
            to: LocalDate?,
            zone: ZoneId,
            now: Instant,
        ): ReportPeriod {
            check(from, to, LocalDate.ofInstant(now, zone))
            val end = to?.plusDays(1)?.atStartOfDay(zone)?.toInstant() ?: now
            val start = from?.atStartOfDay(zone)?.toInstant() ?: (end - DEFAULT_LENGTH)
            return ReportPeriod(start, end)
        }

        private fun check(
            from: LocalDate?,
            to: LocalDate?,
            today: LocalDate,
        ) {
            if (from == null) return
            val last = to ?: today
            if (last.isBefore(from)) throw InvalidReportPeriod("to", "must not be before from")
            if (ChronoUnit.DAYS.between(from, last) > LONGEST_DAYS) {
                throw InvalidReportPeriod("from", "the period must not be longer than $LONGEST_DAYS days")
            }
        }
    }
}
