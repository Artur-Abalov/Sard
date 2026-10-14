// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import dev.sard.server.runs.Action
import dev.sard.server.runs.RunState
import dev.sard.server.runs.RunView
import dev.sard.server.runs.StepState
import dev.sard.server.runs.StepView
import dev.sard.server.runs.Trigger
import dev.sard.server.scheduler.CatchUpPeriod
import tools.jackson.core.type.TypeReference
import tools.jackson.databind.json.JsonMapper
import dev.sard.server.runs.RunKind as DomainRunKind

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
            view.backup?.let {
                BackupOutput(it.snapshotId, it.totalBytes, it.addedBytes, it.repositoryId, it.partial, it.restorable)
            },
            view.restore?.let(RestoreMapping::output),
            view.queuedAt,
            view.dispatchedAt,
            view.startedAt,
            view.finishedAt,
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

/** The restore facts of a run and a step, under the wire names. */
internal object RestoreMapping {
    fun runRestore(restore: dev.sard.server.runs.RestoreInfo) =
        RunRestore(
            restore.snapshotId,
            restore.snapshotCreatedAt,
            restore.repositoryName,
            JSON.readValue(restore.config, CONFIG),
            restore.target,
        )

    fun output(result: dev.sard.server.runs.RestoreResult) =
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
}
