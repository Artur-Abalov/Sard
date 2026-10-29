// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents.dispatch

import dev.sard.server.agents.stream.SendResult
import dev.sard.server.runs.StepView
import org.slf4j.LoggerFactory
import java.util.UUID
import java.util.concurrent.ConcurrentHashMap

private val log = LoggerFactory.getLogger(CancelSteps::class.java)

/**
 * Sends the CancelStep of steps whose run a person cancelled (J1a), once per session of the agent: a send that the
 * session queued is remembered until the session ends ([forget]); one it refused is tried again at the next check.
 * The caller holds the agent's lock, so a CancelStep never overtakes the RunStep of the same session.
 */
internal class CancelSteps(
    private val links: AgentLinks,
) {
    /** The steps a CancelStep was queued for on their agent's current session, with the agent. */
    private val sent = ConcurrentHashMap<UUID, UUID>()

    /** A CancelStep for each of [steps] whose run is cancelled and that has none on this session yet. */
    fun send(
        agentId: UUID,
        steps: List<StepView>,
    ) {
        for (step in steps.filter { it.cancelRequested && !sent.containsKey(it.id) }) {
            val result = links.cancel(agentId, step.id.toString())
            if (result == SendResult.Queued) sent[step.id] = agentId
            log.info("cancel of step {} sent to agent {}: {}", step.id, agentId, result)
        }
    }

    /** The agent's session ended or began: the next [send] sends again. */
    fun forget(agentId: UUID) {
        sent.values.removeAll { it == agentId }
    }
}
