// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import org.springframework.stereotype.Component
import java.util.UUID

// The stage 1 contract before its implementation: every endpoint answers 501
// (ADR 0019). W1b, S2b and S8b replace these one at a time with real beans.

@Component
class UnimplementedSessionApi : SessionApi {
    override fun createSession(request: SessionRequest) = notImplemented()

    override fun getSession(): Session = notImplemented()

    override fun deleteSession() = notImplemented()
}

@Component
class UnimplementedAgentsApi : AgentsApi {
    override fun listAgents(
        status: AgentStatus?,
        cursor: String?,
        limit: Int,
    ): AgentPage = notImplemented()

    override fun getAgent(agentId: UUID): AgentDetails = notImplemented()
}

@Component
class UnimplementedEnrollmentTokensApi : EnrollmentTokensApi {
    override fun createEnrollmentToken(request: CreateEnrollmentTokenRequest): CreatedEnrollmentToken = notImplemented()

    override fun listEnrollmentTokens(
        status: EnrollmentTokenStatus?,
        cursor: String?,
        limit: Int,
    ): EnrollmentTokenPage = notImplemented()

    override fun getEnrollmentToken(tokenId: UUID): EnrollmentToken = notImplemented()

    override fun revokeEnrollmentToken(tokenId: UUID): EnrollmentToken = notImplemented()
}

@Component
class UnimplementedSourcesApi : SourcesApi {
    override fun listSources(
        agentId: UUID?,
        cursor: String?,
        limit: Int,
    ): SourcePage = notImplemented()

    override fun createSource(source: SourceInput): Source = notImplemented()

    override fun getSource(sourceId: UUID): Source = notImplemented()

    override fun replaceSource(
        sourceId: UUID,
        source: SourceInput,
    ): Source = notImplemented()

    override fun deleteSource(sourceId: UUID) = notImplemented()

    override fun startRun(sourceId: UUID): Run = notImplemented()

    override fun listSourceSnapshots(
        sourceId: UUID,
        cursor: String?,
        limit: Int,
    ): SnapshotPage = notImplemented()
}

@Component
class UnimplementedRunsApi : RunsApi {
    override fun listRuns(
        sourceId: UUID?,
        agentId: UUID?,
        status: List<RunStatus>?,
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
