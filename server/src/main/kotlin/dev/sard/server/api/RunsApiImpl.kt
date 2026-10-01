// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import dev.sard.server.extension.TenantResolver
import dev.sard.server.persistence.PageKey
import dev.sard.server.runs.RunFilter
import dev.sard.server.runs.Runs
import dev.sard.server.runs.StepLogs
import org.springframework.stereotype.Component
import java.time.Instant
import java.util.UUID

private const val MAX_LOG_LIMIT = 1000

/** The run endpoints over [Runs], in the tenant of the session. */
@Component
class RunsApiImpl(
    private val runs: Runs,
    private val logs: StepLogs,
    private val tenants: TenantResolver,
) : RunsApi {
    override fun listRuns(
        sourceId: UUID?,
        agentId: UUID?,
        status: List<RunStatus>?,
        queuedFrom: Instant?,
        queuedTo: Instant?,
        cursor: String?,
        limit: Int,
    ): RunPage {
        val page = pageRequest(CursorKind.RUNS, cursor, limit)
        if (queuedFrom != null && queuedTo != null && queuedFrom.isAfter(queuedTo)) {
            throw RequestInvalid("queuedFrom", "must not be later than queuedTo")
        }
        val filter = RunFilter(sourceId, agentId, status.orEmpty().map(RunMapping::state).toSet(), queuedFrom, queuedTo)
        val rows = runs.list(tenants.currentTenantId(), filter, page.after, page.fetch)
        val slice = page.slice(rows) { PageKey(it.queuedAt, it.id) }
        return RunPage(slice.items.map(RunMapping::summary), slice.nextCursor)
    }

    override fun getRun(runId: UUID): Run {
        val run = runs.get(tenants.currentTenantId(), runId) ?: throw ResourceNotFound()
        return RunMapping.run(run)
    }

    override fun listStepLogs(
        runId: UUID,
        stepId: UUID,
        afterSeq: Long,
        limit: Int,
    ): LogPage {
        requireLogPage(afterSeq, limit)
        val read = logs.read(tenants.currentTenantId(), runId, stepId, afterSeq, limit) ?: throw ResourceNotFound()
        val lines = read.lines.map { LogLine(it.seq, it.time, LogLevel.valueOf(it.level.uppercase()), it.text) }
        return LogPage(lines, read.lines.lastOrNull()?.seq ?: afterSeq, read.hasMore, read.truncated)
    }

    private fun requireLogPage(
        afterSeq: Long,
        limit: Int,
    ) {
        if (limit !in 1..MAX_LOG_LIMIT) throw RequestInvalid("limit", "must be between 1 and $MAX_LOG_LIMIT")
        if (afterSeq < 0) throw RequestInvalid("afterSeq", "must not be negative")
    }
}
