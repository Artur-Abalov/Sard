// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import dev.sard.server.extension.TenantResolver
import dev.sard.server.fleet.Repositories
import dev.sard.server.persistence.PageKey
import dev.sard.server.persistence.TenantSessions
import dev.sard.server.runs.StageState
import dev.sard.server.runs.StepOutput
import dev.sard.server.verify.FingerprintRow
import dev.sard.server.verify.JobReason
import dev.sard.server.verify.Misfit
import dev.sard.server.verify.MismatchWording
import dev.sard.server.verify.OpenJob
import dev.sard.server.verify.ReportEntry
import dev.sard.server.verify.SourceVerification
import dev.sard.server.verify.Unsuitable
import dev.sard.server.verify.VerificationCancels
import dev.sard.server.verify.VerificationHint
import dev.sard.server.verify.VerificationReads
import dev.sard.server.verify.VerificationReport
import dev.sard.server.verify.VerificationReports
import dev.sard.server.verify.VerificationStatus
import dev.sard.server.verify.VerificationView
import dev.sard.server.verify.Verifications
import dev.sard.server.verify.VerifierView
import dev.sard.server.verify.misfitsOf
import org.springframework.boot.info.BuildProperties
import org.springframework.stereotype.Component
import java.math.BigDecimal
import java.time.Clock
import java.time.LocalDate
import java.util.UUID
import dev.sard.server.verify.VerificationSchedule as Schedule
import dev.sard.server.verify.VerificationScheduleReader as Reader

private const val READ_ONLY_NOTE = "Give the verifier's host a storage account that can only read the repository."

/** The verification endpoints over [Verifications] and [VerificationReads], in the tenant of the session. */
@Component
class VerificationsApiImpl(
    private val verifications: Verifications,
    private val cancels: VerificationCancels,
    private val reads: VerificationReads,
    private val repositories: Repositories,
    private val reports: VerificationReports,
    private val build: BuildProperties,
    private val tenants: TenantResolver,
    private val sessions: TenantSessions,
    private val clock: Clock,
    private val schedule: Schedule,
    private val reader: Reader,
) : VerificationsApi {
    override fun getSourceVerification(sourceId: UUID): SourceVerificationState {
        val state = verifications.state(tenants.currentTenantId(), sourceId)
        return stateOf(state)
    }

    override fun setSourceVerification(
        sourceId: UUID,
        input: SourceVerificationInput,
    ): SourceVerificationState = stateOf(verifications.setEnabled(tenants.currentTenantId(), sourceId, input.enabled))

    override fun listSourceVerifications(
        sourceId: UUID,
        cursor: String?,
        limit: Int,
    ): VerificationPage {
        val page = pageRequest(CursorKind.VERIFICATIONS, cursor, limit)
        val rows = reads.history(tenants.currentTenantId(), sourceId, page.after, page.fetch)
        val slice = page.slice(rows) { PageKey(it.verifiedAt, it.id) }
        return VerificationPage(slice.items.map(::verificationOf), slice.nextCursor)
    }

    override fun verifyNow(
        sourceId: UUID,
        input: VerifyNowInput,
    ) = jobOf(verifications.verifyNow(tenants.currentTenantId(), sourceId, input.snapshotId))

    override fun getVerification(verificationId: UUID): Verification =
        reads.get(tenants.currentTenantId(), verificationId)?.let(::verificationOf) ?: throw ResourceNotFound()

    override fun cancelJob(jobId: UUID): SourceVerificationState {
        val tenant = tenants.currentTenantId()
        return stateOf(verifications.state(tenant, cancels.cancel(tenant, jobId)))
    }

    override fun getVerificationSchedule(): VerificationSchedule =
        sessions.inTenant(tenants.currentTenantId()) { session ->
            val current = reader.current(session)
            VerificationSchedule(
                current.cron,
                current.timezone,
                Reader.moments(current, clock.instant()).next,
                current.staleAfterDays,
            )
        }

    override fun setVerificationSchedule(input: VerificationScheduleInput): VerificationSchedule {
        val tenant = tenants.currentTenantId()
        val stale = input.staleAfterDays?.let(::wholeDays)
        val now = clock.instant()
        val set = sessions.inTenant(tenant) { schedule.set(it, tenant, input.cron, input.timezone, stale, now) }
        return VerificationSchedule(set.cron, set.timezone, Reader.moments(set, set.since).next, set.staleAfterDays)
    }

    override fun getVerificationReport(
        sourceId: UUID,
        from: LocalDate?,
        to: LocalDate?,
    ): VerificationReportResponse {
        val report = reports.of(tenants.currentTenantId(), sourceId, from, to)
        return reportOf(report, build.serverVersion())
    }

    override fun getVerificationSetup(): VerificationSetup {
        val setup = repositories.firstVerifierSetup(tenants.currentTenantId())
        return VerificationSetup(
            tokenRole = AgentRole.VERIFIER,
            repositories =
                setup.repositories.map {
                    SetupRepository(
                        it.sourceAgentId,
                        it.sourceHostname,
                        it.repositoryName,
                        it.keyAddCommand,
                    )
                },
            verifierRepoAdd = setup.verifierRepoAdd,
            readOnlyNote = READ_ONLY_NOTE,
        )
    }

    override fun listVerifiers(sourceId: UUID?): List<Verifier> {
        val verifiers = reads.verifiers(tenants.currentTenantId(), sourceId)
        return verifiers.map(::verifierOf)
    }
}

private fun stateOf(state: SourceVerification) =
    SourceVerificationState(
        enabled = state.summary.enabled,
        status = state.summary.status?.let(::outcomeOf),
        lastVerifiedAt = state.summary.lastVerifiedAt,
        lastVerifiedLevel = state.summary.lastVerifiedLevel,
        staleSince = state.summary.staleSince,
        openJob = state.openJob?.let(::jobOf),
        hints = state.hints.map(::hintOf),
    )

internal fun jobOf(job: OpenJob) =
    VerificationJob(
        id = job.id,
        status = if (job.running) VerificationJobStatus.RUNNING else VerificationJobStatus.QUEUED,
        reason =
            when (job.reason) {
                JobReason.CYCLE -> VerificationJobReason.CYCLE
                JobReason.MANUAL -> VerificationJobReason.MANUAL
            },
        createdAt = job.createdAt,
        deadline = job.deadline,
        failedAttempts = job.failedAttempts,
        unsuitable = job.unsuitable?.let(::misfitsOf)?.map(::misfitOf),
        cancellation = job.cancellation?.let(CancelMapping::cancellation),
    )

private fun verificationOf(view: VerificationView) =
    Verification(
        id = view.id,
        jobId = view.jobId,
        sourceId = view.sourceId,
        snapshotId = view.snapshotRef,
        resticSnapshotId = view.snapshotId,
        stepId = view.stepId,
        verifierAgentId = view.verifierId,
        keyHolders = view.keyHolders,
        status = outcomeOf(view.status),
        level = view.level,
        reason = view.reason,
        stages =
            view.stages.map {
                VerificationStage(
                    it.level,
                    it.name,
                    it.state?.let(::stageStatusOf),
                    it.startedAt,
                    it.finishedAt,
                    it.exitCode,
                    it.output,
                    it.imageDigest,
                )
            },
        mismatches = view.mismatches.map(::mismatchOf),
        unsuitable = view.unsuitable?.map(::misfitOf),
        restoreDurationMs = view.restoreMillis,
        durationMs = view.durationMillis,
        recovered = view.recovered,
        verifiedAt = view.verifiedAt,
        fingerprint = view.fingerprint.rows.map(::fieldOf),
        hiddenFacts = view.fingerprint.hidden,
        cancelledBy = view.cancelledBy,
        cancelledAt = view.cancelledAt,
    )

private fun verifierOf(view: VerifierView) =
    Verifier(
        agentId = view.candidate.agentId,
        hostname = view.candidate.hostname,
        online = view.candidate.online,
        reported = view.candidate.reported,
        freeBytes = view.candidate.freeBytes,
        sandbox = view.sandbox?.let { stored -> SandboxRuntime.entries.single { it.name.lowercase() == stored } },
        sandboxVersion = view.sandboxVersion,
        repositoryIds =
            view.candidate.repositories.keys
                .sorted(),
        verifyPlugins = view.candidate.verifyPlugins.sorted(),
        busy = view.candidate.busy,
        recentVerifications = view.candidate.recentVerifications,
        verificationsDone = view.done,
        infrastructureFailures = view.infrastructureFailures,
        reasons = view.reasons?.map(::reasonOf)?.sortedBy { it.ordinal },
    )

private fun misfitOf(misfit: Misfit) =
    VerifierMisfit(misfit.agentId, misfit.hostname, misfit.reasons.map(::reasonOf).sortedBy { it.ordinal })

/** Exhaustive mappings, no `else`: a new domain value fails the build here, not in production. */
internal fun outcomeOf(status: VerificationStatus): VerificationOutcome =
    when (status) {
        VerificationStatus.PASSED -> VerificationOutcome.PASSED
        VerificationStatus.FAILED -> VerificationOutcome.FAILED
        VerificationStatus.CANNOT_VERIFY -> VerificationOutcome.CANNOT_VERIFY
        VerificationStatus.INFRASTRUCTURE_FAILURE -> VerificationOutcome.INFRASTRUCTURE_FAILURE
        VerificationStatus.NO_VERIFIER -> VerificationOutcome.NO_VERIFIER
        VerificationStatus.CANCELLED -> VerificationOutcome.CANCELLED
    }

/** The same names on both sides (VerifierChoiceTest pins the domain's); a renamed one fails at the first use. */
private fun reasonOf(reason: Unsuitable): VerifierReason = VerifierReason.valueOf(reason.name)

private fun stageStatusOf(state: StageState): VerificationStageStatus =
    when (state) {
        StageState.PASSED -> VerificationStageStatus.PASSED
        StageState.FAILED -> VerificationStageStatus.FAILED
        StageState.CANNOT_RUN -> VerificationStageStatus.CANNOT_RUN
        StageState.NOT_APPLICABLE -> VerificationStageStatus.NOT_APPLICABLE
    }
