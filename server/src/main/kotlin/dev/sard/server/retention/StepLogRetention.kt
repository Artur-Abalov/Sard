// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.retention

import org.springframework.jdbc.core.ConnectionCallback
import org.springframework.jdbc.core.JdbcTemplate
import java.time.Duration
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
 * once its upper bound is not later than the boundary of the term, so every line in it is older than the term.
 */
class StepLogRetention(
    private val jdbc: JdbcTemplate,
    private val lockTimeout: Duration,
) {
    /** The partitions whose every line is older than the boundary [cutoff], oldest first. */
    fun expired(cutoff: Instant): List<String> =
        jdbc
            .queryForList(PARTITIONS, String::class.java)
            .filterNotNull()
            .filter { name -> partitionEnd(name)?.let { it <= cutoff } == true }
            .sorted()

    /**
     * Drops [name]; false if another server dropped it first. The partitions are dropped one at a time, serialised on
     * `step_logs` in a mode that lets lines be written meanwhile, so two servers never both count the same drop. A
     * partition held by another transaction longer than the lock timeout fails the drop.
     */
    fun drop(name: String): Boolean {
        require(PARTITION_NAME.matches(name)) { "not a log partition: $name" }
        return checkNotNull(
            jdbc.execute(
                ConnectionCallback { connection ->
                    connection.autoCommit = false
                    try {
                        connection.createStatement().use { it.execute("set local lock_timeout = ${lockTimeout.toMillis()}") }
                        connection.createStatement().use { it.execute("lock table step_logs in share update exclusive mode") }
                        val attached =
                            connection.prepareStatement(ATTACHED).use {
                                it.setString(1, name)
                                it.executeQuery().use { rows -> rows.next() && rows.getBoolean(1) }
                            }
                        // The name matched the pattern above: digits only.
                        if (attached) connection.createStatement().use { it.execute("drop table $name") }
                        connection.commit()
                        attached
                    } catch (e: Exception) {
                        connection.rollback()
                        throw e
                    } finally {
                        connection.autoCommit = true
                    }
                },
            ),
        )
    }
}

private const val PARTITIONS =
    "select c.relname from pg_inherits i join pg_class c on c.oid = i.inhrelid where i.inhparent = 'step_logs'::regclass"

private const val ATTACHED =
    "select exists (select 1 from pg_inherits where inhparent = 'step_logs'::regclass and inhrelid = to_regclass(?))"
