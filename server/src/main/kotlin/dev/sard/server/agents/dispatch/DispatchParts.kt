// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents.dispatch

import dev.sard.proto.agent.v1.RunStep
import dev.sard.server.agents.stream.ConnectedAgent
import dev.sard.server.agents.stream.SendResult
import dev.sard.server.runs.Action
import dev.sard.server.runs.StepView
import dev.sard.server.runs.WaitingSteps
import java.util.UUID
import dev.sard.proto.agent.v1.Action as ProtoAction

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

/** A step as the agent receives it (agent.proto RunStep); no timeout (the agent's maximum) and no tags. */
object RunSteps {
    fun of(step: StepView): RunStep =
        RunStep
            .newBuilder()
            .setCommandId(step.id.toString())
            .setPlugin(step.plugin)
            .setConfigJson(step.config)
            .setAction(actionOf(step.action))
            .setRepositoryName(step.repositoryName.orEmpty())
            .build()

    private fun actionOf(action: Action): ProtoAction =
        when (action) {
            Action.BACKUP -> ProtoAction.ACTION_BACKUP
            Action.RESTORE -> ProtoAction.ACTION_RESTORE
            Action.VERIFY -> ProtoAction.ACTION_VERIFY
            Action.RUN -> ProtoAction.ACTION_RUN
        }
}
