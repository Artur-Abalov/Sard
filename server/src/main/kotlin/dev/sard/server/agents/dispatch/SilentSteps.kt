// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents.dispatch

import dev.sard.server.runs.DispatchLedger
import dev.sard.server.runs.SilentStep
import org.slf4j.LoggerFactory
import java.time.Duration
import java.util.UUID

private val log = LoggerFactory.getLogger(SilentSteps::class.java)

/**
 * A running step whose agent sent nothing past the timeout and the lost [window] is closed as timed out (J1b, Р-J7),
 * and an online agent is told to stop it, so that it does not go on after the run has failed (КJ-6).
 */
internal class SilentSteps(
    private val ledger: DispatchLedger,
    private val links: AgentLinks,
    private val window: Duration,
) {
    /** Closes [step] and cancels it on its agent under the agent's lock ([locked]). Never throws. */
    fun timeOut(
        step: SilentStep,
        locked: (UUID, () -> Unit) -> Unit,
    ) {
        runCatching {
            if (!ledger.timedOutBySilence(step.tenantId, step.id, window)) return
            log.warn("step {} timed out: its agent sent no result within the timeout and the window", step.id)
            if (links.online(step.agentId)) locked(step.agentId) { links.cancel(step.agentId, step.id.toString()) }
        }.onFailure { log.warn("timing out step {} did not finish: {}", step.id, it.toString()) }
    }
}
