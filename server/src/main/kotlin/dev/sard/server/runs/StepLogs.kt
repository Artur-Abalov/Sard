// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.runs

import dev.sard.server.persistence.TenantSessions
import java.sql.Connection
import java.sql.PreparedStatement
import java.sql.ResultSet
import java.sql.Types
import java.time.Clock
import java.time.Instant
import java.time.ZoneOffset
import java.util.UUID

// Native SQL names tenant_id explicitly (ADR 0013, rule 8). The row lock orders appends of one step.
private const val LOCK =
    "select log_lines, log_bytes, log_truncated from run_steps " +
        "where tenant_id = ? and id = ? and agent_id = ? for update"
private const val INSERT =
    "insert into step_logs (tenant_id, step_id, seq, received_at, time, level, text) values (?, ?, ?, ?, ?, ?, ?)"
private const val COUNT =
    "update run_steps set log_lines = log_lines + ?, log_bytes = log_bytes + ?, log_truncated = log_truncated or ? " +
        "where tenant_id = ? and id = ?"

/** What [StepLogs.append] did with a chunk: lines kept, lines dropped past the limit, the mark written now. */
data class Appended(
    val kept: Int,
    val dropped: Int,
    val marked: Boolean,
)

/**
 * The log of an agent's own step (S7a): lines numbered by the server (LogChunk carries no
 * sequence) from `run_steps.log_lines`, within [LogLimits] (see [LogBudget]), received at the
 * server's clock, which also picks the partition. A closed step keeps taking lines: the agent
 * sends a step's last lines after its result.
 */
class StepLogs(
    private val sessions: TenantSessions,
    private val clock: Clock,
    private val limits: LogLimits,
) {
    private data class Kept(
        val lines: Long,
        val bytes: Long,
        val truncated: Boolean,
    )

    /** Null when no step of this agent has this id, in its tenant: nothing written. */
    fun append(
        tenantId: UUID,
        agentId: UUID,
        stepId: UUID,
        lines: List<LogLine>,
    ): Appended? =
        sessions.inTenant(tenantId) { session ->
            session.doReturningWork<Appended?> { connection: Connection ->
                connection.kept(tenantId, stepId, agentId)?.let { kept ->
                    val fit = LogBudget.fit(lines, kept.bytes, kept.truncated, limits)
                    val written = fit.kept + listOfNotNull(LogBudget.mark(limits).takeIf { fit.mark })
                    connection.write(tenantId, stepId, kept.lines, written)
                    connection.count(tenantId, stepId, written.size, fit)
                    Appended(fit.kept.size, lines.size - fit.kept.size, fit.mark)
                }
            }
        }

    private fun Connection.kept(
        tenantId: UUID,
        stepId: UUID,
        agentId: UUID,
    ): Kept? =
        prepareStatement(LOCK).use { query ->
            query.bind(tenantId, stepId, agentId)
            query.executeQuery().use { row -> if (row.next()) row.kept() else null }
        }

    private fun Connection.write(
        tenantId: UUID,
        stepId: UUID,
        after: Long,
        lines: List<LogLine>,
    ) {
        if (lines.isEmpty()) return
        val now = utc(clock.instant())
        prepareStatement(INSERT).use { insert ->
            lines.forEachIndexed { i, line ->
                insert.bind(tenantId, stepId, after + i + 1, now, line.time?.let(::utc), line.level, line.text)
                insert.addBatch()
            }
            insert.executeBatch()
        }
    }

    private fun Connection.count(
        tenantId: UUID,
        stepId: UUID,
        written: Int,
        fit: LogFit,
    ) {
        if (written == 0) return
        prepareStatement(COUNT).use { update ->
            update.bind(written.toLong(), fit.bytes, fit.mark, tenantId, stepId)
            update.executeUpdate()
        }
    }

    private fun ResultSet.kept() = Kept(getLong("log_lines"), getLong("log_bytes"), getBoolean("log_truncated"))

    private fun utc(instant: Instant) = instant.atOffset(ZoneOffset.UTC)

    /** Binds [values] to the statement's placeholders in order; null as SQL NULL of the column's type. */
    private fun PreparedStatement.bind(vararg values: Any?) =
        values.forEachIndexed { i, value ->
            if (value == null) setNull(i + 1, Types.NULL) else setObject(i + 1, value)
        }
}
