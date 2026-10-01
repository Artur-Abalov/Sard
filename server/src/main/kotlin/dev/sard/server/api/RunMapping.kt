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
            view.backup?.let { BackupOutput(it.snapshotId, it.totalBytes, it.addedBytes, it.repositoryId) },
            view.queuedAt,
            view.dispatchedAt,
            view.startedAt,
            view.finishedAt,
        )

    private fun trigger(trigger: Trigger): RunTrigger =
        when (trigger) {
            Trigger.SCHEDULE -> RunTrigger.SCHEDULE
            Trigger.MANUAL -> RunTrigger.MANUAL
            Trigger.VERIFICATION -> RunTrigger.VERIFICATION
        }

    private fun status(state: RunState): RunStatus =
        when (state) {
            RunState.QUEUED -> RunStatus.QUEUED
            RunState.DISPATCHED -> RunStatus.DISPATCHED
            RunState.RUNNING -> RunStatus.RUNNING
            RunState.SUCCEEDED -> RunStatus.SUCCEEDED
            RunState.FAILED -> RunStatus.FAILED
            RunState.CANCELLED -> RunStatus.CANCELLED
        }

    private fun action(action: Action): StepAction =
        when (action) {
            Action.BACKUP -> StepAction.BACKUP
            Action.RESTORE -> StepAction.RESTORE
            Action.VERIFY -> StepAction.VERIFY
            Action.RUN -> StepAction.RUN
        }

    private fun stepStatus(state: StepState): StepStatus =
        when (state) {
            StepState.QUEUED -> StepStatus.QUEUED
            StepState.DISPATCHED -> StepStatus.DISPATCHED
            StepState.RUNNING -> StepStatus.RUNNING
            StepState.SUCCEEDED -> StepStatus.SUCCEEDED
            StepState.FAILED -> StepStatus.FAILED
            StepState.CANCELLED -> StepStatus.CANCELLED
            StepState.TIMED_OUT -> StepStatus.TIMED_OUT
            StepState.REJECTED -> StepStatus.REJECTED
            StepState.LOST -> StepStatus.LOST
        }
}
