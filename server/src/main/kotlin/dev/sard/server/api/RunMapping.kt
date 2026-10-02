// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import dev.sard.server.runs.Action
import dev.sard.server.runs.RunState
import dev.sard.server.runs.RunView
import dev.sard.server.runs.StepState
import dev.sard.server.runs.StepView
import dev.sard.server.runs.Trigger

/** Domain runs and steps to the contract's classes: the same facts, under the wire names. */
internal object RunMapping {
    fun run(view: RunView) =
        Run(
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
        )

    fun summary(view: RunView) =
        RunSummary(
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
        )

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
            view.backup?.let { BackupOutput(it.snapshotId, it.totalBytes, it.addedBytes, it.repositoryId, it.partial) },
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

    private fun action(action: Action): StepAction = StepAction.valueOf(action.name)

    private fun stepStatus(state: StepState): StepStatus = StepStatus.valueOf(state.name)
}
