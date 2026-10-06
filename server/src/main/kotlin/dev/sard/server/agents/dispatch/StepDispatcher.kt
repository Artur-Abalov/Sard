// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents.dispatch

import dev.sard.server.agents.stream.ConnectedAgent
import dev.sard.server.agents.stream.SendResult
import dev.sard.server.agents.stream.StreamCloseReason
import dev.sard.server.runs.DispatchLedger
import dev.sard.server.runs.LostDeadlines
import dev.sard.server.runs.StepState
import dev.sard.server.runs.StepView
import dev.sard.server.runs.WaitingSteps
import org.slf4j.LoggerFactory
import java.time.Clock
import java.time.Duration
import java.time.Instant
import java.util.UUID
import java.util.concurrent.ConcurrentHashMap

private val log = LoggerFactory.getLogger(StepDispatcher::class.java)

/**
 * Sends steps to agents and reconciles them on Hello (ADR 0038). Each step is claimed in
 * the database before it is sent and released when the session refuses it, so two paths never
 * send one step; the sends themselves run outside any transaction. Work on one agent is
 * serialized; every write is in that agent's tenant.
 *
 * A dispatched or running step is lost when its lost deadline (`run_steps.lost_deadline`, FXs)
 * comes: one window after the agent's session ended, after the server started or after a Hello
 * that did not list it, whichever is earliest; a Hello that lists it, progress, a result or a
 * send clears it. The check reads the deadlines across tenants and moves each step in its own.
 */
class StepDispatcher(
    private val ledger: DispatchLedger,
    private val deadlines: LostDeadlines,
    private val links: AgentLinks,
    private val clock: Clock,
    private val lostAfter: Duration,
    private val metrics: DispatchMetrics,
    private val waiting: () -> WaitingSteps,
) {
    private val locks = ConcurrentHashMap<UUID, Any>()

    /** A run was committed (Runs.start): send its step now if the agent is online. Never throws. */
    fun onQueued(
        tenantId: UUID,
        agentId: UUID,
    ) {
        if (!links.online(agentId)) return
        runCatching { deliverQueued(tenantId, agentId) }
            .onFailure { log.warn("queued steps of agent {} wait for the next check: {}", agentId, it.toString()) }
    }

    /**
     * Hello: listed steps are left alone and lose their deadline; queued ones are sent in creation
     * order; a dispatched one sent before this Hello is sent again; a running one is lost unless a
     * result closes it within [lostAfter] of this Hello, or of an earlier deadline.
     */
    fun onHello(
        agent: ConnectedAgent,
        running: List<String>,
    ) = locked(agent.agentId) {
        val helloAt = clock.instant()
        val (listed, absent) = ledger.active(agent.tenantId, agent.agentId).partition { it.id.toString() in running }
        val (started, waiting) = absent.partition { it.status == StepState.RUNNING }
        deadlines.confirm(agent.tenantId, agent.agentId, listed.map { it.id })
        deadlines.expect(agent.tenantId, agent.agentId, started.map { it.id }, helloAt + lostAfter)
        deliver(agent, waiting, helloAt)
    }

    /**
     * The agent's session ended: its steps in flight are lost within [lostAfter] unless it comes
     * back. Not when the server itself stops: the start gives them a fresh window. Never throws.
     */
    fun onDisconnected(
        agent: ConnectedAgent,
        reason: String,
    ) {
        if (reason == StreamCloseReason.SERVER_SHUTTING_DOWN.name) return
        locked(agent.agentId) {
            runCatching { deadlines.expectAll(agent.tenantId, agent.agentId, clock.instant() + lostAfter) }
                .onFailure { log.error("steps of agent {} have no lost deadline: {}", agent.agentId, it.toString()) }
        }
    }

    /** The server starts, before any agent can connect: every step in flight gets a fresh window. */
    fun onStart() {
        val deadline = clock.instant() + lostAfter
        for (agent in deadlines.holders()) {
            locked(agent.id) { deadlines.restart(agent.tenantId, agent.id, deadline) }
        }
    }

    /** Marks steps whose deadline passed, retries queued steps of online agents, refreshes the metric. */
    fun tick() {
        for (step in deadlines.overdue(clock.instant())) {
            if (ledger.lost(step.tenantId, step.id)) log.warn("step {} lost: its agent did not report it", step.id)
        }
        for (agent in links.connected()) {
            if (links.online(agent.agentId)) deliverQueued(agent.tenantId, agent.agentId)
        }
        metrics.waiting(waiting())
    }

    private fun deliverQueued(
        tenantId: UUID,
        agentId: UUID,
    ) = locked(agentId) {
        val queued = ledger.active(tenantId, agentId).filter { it.status == StepState.QUEUED }
        deliver(ConnectedAgent(agentId, tenantId, ""), queued, sentBefore = null)
    }

    /** Sends [steps] in order and stops at the first one the session refuses. */
    private fun deliver(
        agent: ConnectedAgent,
        steps: List<StepView>,
        sentBefore: Instant?,
    ) {
        for (step in steps) {
            if (!send(agent, step, sentBefore)) return
        }
    }

    /** False when the session refused: the step is back in the queue and the round ends. */
    private fun send(
        agent: ConnectedAgent,
        step: StepView,
        sentBefore: Instant?,
    ): Boolean {
        if (!take(agent, step, sentBefore)) return true
        val result = links.send(agent.agentId, RunSteps.of(step))
        if (result != SendResult.Queued) giveBack(agent, step, result)
        return result == SendResult.Queued
    }

    private fun giveBack(
        agent: ConnectedAgent,
        step: StepView,
        result: SendResult,
    ) {
        ledger.release(agent.tenantId, step.id)
        metrics.refused()
        log.info("step {} back in the queue of agent {}: {}", step.id, agent.agentId, result)
    }

    private fun take(
        agent: ConnectedAgent,
        step: StepView,
        sentBefore: Instant?,
    ): Boolean {
        if (step.status == StepState.QUEUED) return ledger.claim(agent.tenantId, step.id)
        val again = sentBefore != null && ledger.redispatch(agent.tenantId, step.id, sentBefore)
        if (again) {
            metrics.redispatched()
            log.info("step {} sent again to agent {}: dispatched, but absent from its Hello", step.id, agent.agentId)
        }
        return again
    }

    private fun <T> locked(
        agentId: UUID,
        work: () -> T,
    ): T = synchronized(locks.computeIfAbsent(agentId) { Any() }) { work() }
}
