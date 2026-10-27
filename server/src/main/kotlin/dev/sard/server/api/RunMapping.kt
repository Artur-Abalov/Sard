// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import dev.sard.server.runs.Action
import dev.sard.server.runs.BackupResult
import dev.sard.server.runs.FingerprintSummary
import dev.sard.server.runs.RunState
import dev.sard.server.runs.RunView
import dev.sard.server.runs.StepState
import dev.sard.server.runs.StepView
import dev.sard.server.runs.Trigger
import dev.sard.server.scheduler.CatchUpPeriod
import tools.jackson.core.type.TypeReference
import tools.jackson.databind.json.JsonMapper
import dev.sard.server.runs.Cancellation as DomainCancellation
import dev.sard.server.runs.RestoreInfo as DomainRestoreInfo
import dev.sard.server.runs.RestoreResult as DomainRestoreResult
import dev.sard.server.runs.RunKind as DomainRunKind
import dev.sard.server.runs.RunOutcome as DomainRunOutcome

private val JSON = JsonMapper.builder().build()
private val CONFIG = object : TypeReference<Map<String, Any?>>() {}

/** Domain runs and steps to the contract's classes: the same facts, under the wire names. */
internal object RunMapping {
    fun run(
        view: RunView,
        period: CatchUpPeriod?,
    ) = Run(
        view.id,
        view.sourceId,
        view.sourceName,
        view.sourceDeleted,
        view.agentId,
        trigger(view.trigger),
        status(view.status),
        view.message,
        view.queuedAt,
        view.startedAt,
        view.finishedAt,
        view.steps.map(::step),
        catchUp(period),
        kind(view.kind),
        view.requestedBy,
        view.restore?.let(RestoreMapping::runRestore),
        CancelMapping.outcome(view.outcome),
        view.cancellation?.let(CancelMapping::cancellation),
    )

    fun summary(
        view: RunView,
        period: CatchUpPeriod?,
    ) = RunSummary(
        view.id,
        view.sourceId,
        view.sourceName,
        view.sourceDeleted,
        view.agentId,
        trigger(view.trigger),
        status(view.status),
        view.message,
        view.queuedAt,
        view.startedAt,
        view.finishedAt,
        catchUp(period),
        kind(view.kind),
        view.requestedBy,
        CancelMapping.outcome(view.outcome),
    )

    private fun catchUp(period: CatchUpPeriod?) =
        period?.let { CatchUp(it.missedFrom, it.missedUntil, it.missedCount, it.missedCountCapped, it.timezone) }

    fun step(view: StepView) =
        RunStep(
            view.id,
            view.ordinal,
            action(view.action),
            stepStatus(view.status),
            view.phase?.let { StepPhase.valueOf(it.uppercase()) },
            view.agentId,
            view.sourceId,
            view.plugin,
            view.repositoryName,
            view.bytesProcessed,
            view.bytesTotal,
            view.filesProcessed,
            view.filesTotal,
            view.message,
            view.backup?.let(BackupMapping::output),
            view.restore?.let(RestoreMapping::output),
            RestoreMapping.outcome(view),
            view.queuedAt,
            view.dispatchedAt,
            view.startedAt,
            view.finishedAt,
            view.lateStatus?.let(::stepStatus),
        )

    // The domain's enums and the contract's have the same constants: one name is one value. RunMappingTest
    // checks every constant of both, so a new one cannot be forgotten on either side.
    private fun trigger(trigger: Trigger): RunTrigger = RunTrigger.valueOf(trigger.name)

    private fun status(state: RunState): RunStatus = RunStatus.valueOf(state.name)

    /** The domain's state of a status a client filters by. */
    fun state(status: RunStatus): RunState = RunState.valueOf(status.name)

    private fun kind(kind: DomainRunKind): RunKind = RunKind.valueOf(kind.name)

    /** The domain's kind of a kind a client filters by. */
    fun kindOf(kind: RunKind): DomainRunKind = DomainRunKind.valueOf(kind.name)

    private fun action(action: Action): StepAction = StepAction.valueOf(action.name)

    private fun stepStatus(state: StepState): StepStatus = StepStatus.valueOf(state.name)
}

/** The outcome of a run and its cancel (J1a), under the wire names. */
internal object CancelMapping {
    fun outcome(outcome: DomainRunOutcome): RunOutcome = RunOutcome.valueOf(outcome.name)

    fun cancellation(cancellation: DomainCancellation) =
        Cancellation(
            cancellation.requestedAt,
            cancellation.requestedBy,
            CancelResult.valueOf(cancellation.result.name),
        )
}

/** The restore facts of a run and a step, under the wire names. */
internal object RestoreMapping {
    fun runRestore(restore: DomainRestoreInfo) =
        RunRestore(
            restore.snapshotId,
            restore.snapshotCreatedAt,
            restore.repositoryName,
            JSON.readValue(restore.config, CONFIG),
            restore.target,
        )

    fun output(result: DomainRestoreResult) =
        RestoreOutput(
            result.target,
            result.filesRestored,
            result.bytesRestored,
            result.database,
            result.tablesRestored,
            result.rolesRestored,
            result.ownersRestored,
            result.leftover,
        )

    /**
     * A restore is all or nothing: a failed step restored nothing unless the agent names what its rollback left.
     */
    fun outcome(step: StepView): RestoreOutcome? =
        when {
            step.action != Action.RESTORE -> null
            !step.restore?.leftover.isNullOrEmpty() -> RestoreOutcome.ROLLBACK_FAILED
            else -> ended(step.status, step.restore, step.lateStatus)
        }

    private fun ended(
        status: StepState,
        result: DomainRestoreResult?,
        lateStatus: StepState?,
    ): RestoreOutcome? =
        when {
            status == StepState.SUCCEEDED -> if (result == null) null else RestoreOutcome.RESTORED
            isLate(status, lateStatus) -> late(result)
            status.active -> null
            else -> RestoreOutcome.NOTHING // failed, rejected, timed out, cancelled: all or nothing
        }

    /** The step was closed by the server (lost, or cancelled without its agent's confirmation) before its result. */
    private fun isLate(
        status: StepState,
        lateStatus: StepState?,
    ) = status == StepState.LOST || (status == StepState.CANCELLED && lateStatus != null)

    // A step closed by the server is late only when its late output says where the data is.
    private fun late(result: DomainRestoreResult?): RestoreOutcome? =
        when {
            result?.target.isNullOrEmpty() -> null
            else -> RestoreOutcome.LATE
        }
}

/** The backup facts of a step, under the wire names. */
internal object BackupMapping {
    fun output(backup: BackupResult) =
        BackupOutput(
            backup.snapshotId,
            backup.totalBytes,
            backup.addedBytes,
            backup.repositoryId,
            backup.partial,
            backup.restorable,
            backup.totalFiles,
            backup.fingerprint?.let(::fingerprint),
            backup.warnings,
        )

    private fun fingerprint(f: FingerprintSummary) =
        Fingerprint(f.sha256, f.format, f.plugin, f.facts.map { FingerprintFact(it.name, it.rule, it.value) }, f.hidden)
}
