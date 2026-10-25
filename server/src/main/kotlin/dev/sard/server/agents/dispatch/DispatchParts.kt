// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents.dispatch

import com.google.protobuf.ByteString
import com.google.protobuf.Duration
import com.google.protobuf.Timestamp
import dev.sard.proto.agent.v1.RunStep
import dev.sard.server.agents.stream.ConnectedAgent
import dev.sard.server.agents.stream.SendResult
import dev.sard.server.runs.Action
import dev.sard.server.runs.StepView
import dev.sard.server.runs.VerifyTask
import dev.sard.server.runs.WaitingSteps
import java.util.UUID
import dev.sard.proto.agent.v1.Action as ProtoAction
import dev.sard.proto.agent.v1.VerifyTask as ProtoVerifyTask

/** The agent sessions as dispatch needs them; over AgentConnections and the registry in production. */
interface AgentLinks {
    /** Agents holding a session slot now. */
    fun connected(): List<ConnectedAgent>

    fun online(agentId: UUID): Boolean

    /** Queues [step] on the agent's session; [SendResult.Queued] is not "delivered" (S5a). */
    fun send(
        agentId: UUID,
        step: RunStep,
    ): SendResult
}

/** `sard.run.steps.*` (S6a). */
interface DispatchMetrics {
    /** A dispatched step sent again after a Hello that did not list it. */
    fun redispatched()

    /** A session refused a step (queue full or no session); the step went back to the queue. */
    fun refused()

    fun waiting(steps: WaitingSteps)
}

/**
 * A step as the agent receives it (agent.proto RunStep); no timeout but a VERIFY step's (the agent's maximum). The tags
 * reach the restic snapshot as `key=value` (FXs Д5): a snapshot names its step, run and source.
 * UUIDs hold no comma, the one character the agent refuses; a script step has no source, its tag
 * is empty (owner decision В5).
 */
object RunSteps {
    const val STEP_TAG = "sard.step"
    const val RUN_TAG = "sard.run"
    const val SOURCE_TAG = "sard.source"

    fun of(step: StepView): RunStep {
        val command =
            RunStep
                .newBuilder()
                .setCommandId(step.id.toString())
                .setPlugin(step.plugin)
                .setConfigJson(step.config)
                .setAction(actionOf(step.action))
                .setRepositoryName(step.repositoryName.orEmpty())
                .setSnapshotId(step.snapshotId.orEmpty())
                .putTags(STEP_TAG, step.id.toString())
                .putTags(RUN_TAG, step.runId.toString())
                .putTags(SOURCE_TAG, step.sourceId?.toString().orEmpty())
        step.verifyTask?.let { verify(command, it) }
        return command.build()
    }

    /**
     * A VERIFY step (V4): its job, the repository's restic id, the fingerprint and the deadline, and the source's
     * machines (V5); it has a timeout.
     */
    private fun verify(
        command: RunStep.Builder,
        task: VerifyTask,
    ) {
        val verify =
            ProtoVerifyTask
                .newBuilder()
                .setJobId(task.jobId.toString())
                .setRepositoryId(task.repositoryId)
                .setFingerprint(ByteString.copyFrom(task.fingerprint ?: ByteArray(0)))
                .setFingerprintSha256(task.fingerprintSha256.orEmpty())
                .setDeadline(Timestamp.newBuilder().setSeconds(task.deadline.epochSecond).setNanos(task.deadline.nano))
                .addAllSourceMachineIds(task.sourceMachineIds)
        command.setVerify(verify).setTimeout(Duration.newBuilder().setSeconds(task.timeoutSeconds))
    }

    private fun actionOf(action: Action): ProtoAction =
        when (action) {
            Action.BACKUP -> ProtoAction.ACTION_BACKUP
            Action.RESTORE -> ProtoAction.ACTION_RESTORE
            Action.VERIFY -> ProtoAction.ACTION_VERIFY
            Action.RUN -> ProtoAction.ACTION_RUN
        }
}
