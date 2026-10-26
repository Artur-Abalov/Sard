// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.retention

import org.springframework.jdbc.core.JdbcTemplate
import java.time.Instant
import java.time.LocalDate
import java.time.ZoneOffset

private val PARTITION_NAME = Regex("""step_logs_(\d{4})_(\d{2})""")

/**
 * The first instant that is no longer inside the monthly partition [name] of `step_logs` (ADR 0013, ADR 0041: months
 * by UTC), or null for a table that is not one of the server's partitions.
 */
fun partitionEnd(name: String): Instant? {
    val match = PARTITION_NAME.matchEntire(name) ?: return null
    val (year, month) = match.destructured
    return LocalDate
        .of(year.toInt(), month.toInt(), 1)
        .plusMonths(1)
        .atStartOfDay()
        .toInstant(ZoneOffset.UTC)
}

/**
 * Drops whole monthly partitions of `step_logs`: log lines leave only this way, never one by one. A partition goes
 * once its upper bound is not later than the boundary of the term, so every line in it is older than the term. The
 * server is sard_app and may not detach or drop a table: `drop_step_log_partition` does it as sard_owner (DB2,
 * ADR 0069), waits for a lock two seconds at most and serialises the servers on one advisory lock.
 */
class StepLogRetention(
    private val jdbc: JdbcTemplate,
) {
    /** The partitions whose every line is older than the boundary [cutoff], oldest first. */
    fun expired(cutoff: Instant): List<String> =
        jdbc
            .queryForList(PARTITIONS, String::class.java)
            .filterNotNull()
            .filter { name -> partitionEnd(name)?.let { it <= cutoff } == true }
            .sorted()

    /**
     * Drops [name]; false if another server dropped it first. A partition that a reader holds for longer than the
     * function's lock timeout fails the drop with SQLSTATE 55P03, which frees the writers of log lines; the caller
     * goes on and the next pass drops it.
     */
    fun drop(name: String): Boolean {
        require(PARTITION_NAME.matches(name)) { "not a log partition: $name" }
        return jdbc.queryForObject("select drop_step_log_partition(?)", Boolean::class.java, name) == true
    }
}

private const val PARTITIONS =
    "select c.relname from pg_inherits i join pg_class c on c.oid = i.inhrelid " +
        "where i.inhparent = 'step_logs'::regclass"
