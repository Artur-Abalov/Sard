// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import org.springframework.http.HttpStatus
import org.springframework.http.ProblemDetail
import org.springframework.stereotype.Component
import org.springframework.web.ErrorResponseException
import java.util.UUID

// The stage 1 contract before its implementation: every endpoint answers 501
// (ADR 0019). S2b and S8b replace these one at a time with real beans; W1b already
// replaced the session endpoints with dev.sard.server.auth.SessionApiImpl.

@Component
class UnimplementedRunsApi : RunsApi {
    override fun listRuns(
        sourceId: UUID?,
        agentId: UUID?,
        status: List<RunStatus>?,
        queuedFrom: java.time.Instant?,
        queuedTo: java.time.Instant?,
        cursor: String?,
        limit: Int,
    ): RunPage = notImplemented()

    override fun getRun(runId: UUID): Run = notImplemented()

    override fun listStepLogs(
        runId: UUID,
        stepId: UUID,
        afterSeq: Long,
        limit: Int,
    ): LogPage = notImplemented()
}

/** Body of a stub whose behavior S8b implements. */
fun notImplemented(): Nothing {
    val problem = ProblemDetail.forStatus(HttpStatus.NOT_IMPLEMENTED)
    problem.setProperty("code", "not_implemented")
    throw ErrorResponseException(HttpStatus.NOT_IMPLEMENTED, problem, null)
}
