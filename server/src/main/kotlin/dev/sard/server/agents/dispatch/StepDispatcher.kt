// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents.dispatch

import dev.sard.server.agents.stream.ConnectedAgent
import dev.sard.server.agents.stream.SendResult
import dev.sard.server.agents.stream.StreamCloseReason
import dev.sard.server.role.AgentRole
import dev.sard.server.runs.DispatchLedger
import dev.sard.server.runs.LostDeadlines
import dev.sard.server.runs.RoleLedger
import dev.sard.server.runs.SilentStep
import dev.sard.server.runs.StepState
import dev.sard.server.runs.StepView
import dev.sard.server.runs.WaitingSteps
import dev.sard.server.runs.allows
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
    roles: RoleLedger,
    private val startGrace: Duration = Duration.ZERO,
    private val waiting: () -> WaitingSteps,
) {
    private val locks = ConcurrentHashMap<UUID, Any>()

    private val cancels = CancelSteps(links)
    private val silent = SilentSteps(ledger, links, lostAfter)
    private val guard = RoleGuard(roles)

    /** A run was committed (Runs.start): send its step now if the agent is online. Never throws. */
    fun onQueued(
        tenantId: UUID,
        agentId: UUID,
    ) {
        if (!links.online(agentId)) return
        runCatching { serve(tenantId, agentId) }
            .onFailure { log.warn("queued steps of agent {} wait for the next check: {}", agentId, it.toString()) }
    }

    /**
     * A run was marked for cancel (RunCancels): its steps in flight get their CancelStep now if the agent is online;
     * an offline agent gets it at its Hello. Never throws.
     */
    fun onCancelled(
        tenantId: UUID,
        agentId: UUID,
    ) {
        if (!links.online(agentId)) return
        runCatching { locked(agentId) { cancels.send(agentId, ledger.active(tenantId, agentId)) } }
            .onFailure { log.warn("cancel of steps of agent {} waits for the next check: {}", agentId, it.toString()) }
    }

    /**
     * Hello: listed steps are left alone and lose their deadline; queued ones are sent in creation
     * order; a dispatched one sent before this Hello is sent again; a running one is lost unless a
     * result closes it within [lostAfter] of this Hello, or of an earlier deadline. A step of a cancelled run is
     * never sent again (J1a, Р-J3): the agent gets its CancelStep, and a dispatched one it does not list has a window.
     */
    fun onHello(
        agent: ConnectedAgent,
        running: List<String>,
    ) = locked(agent.agentId) {
        val helloAt = clock.instant()
        val (listed, absent) = ledger.active(agent.tenantId, agent.agentId).partition { it.id.toString() in running }
        val (started, waiting) = absent.partition { it.status == StepState.RUNNING }
        val (unsent, others) = waiting.partition { it.cancelRequested }
        deadlines.confirm(agent.tenantId, agent.agentId, listed.map { it.id })
        deadlines.expect(agent.tenantId, agent.agentId, (started + unsent).map { it.id }, helloAt + lostAfter)
        cancels.forget(agent.agentId)
        cancels.send(agent.agentId, listed + unsent)
        deliver(agent, others, helloAt)
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
        cancels.forget(agent.agentId)
        locked(agent.agentId) {
            runCatching { deadlines.expectAll(agent.tenantId, agent.agentId, clock.instant() + lostAfter) }
                .onFailure { log.error("steps of agent {} have no lost deadline: {}", agent.agentId, it.toString()) }
        }
    }

    /**
     * The server starts, before any agent can connect: every step in flight gets a fresh window, at
     * least [startGrace] long, so an agent that backs off for a full pause is not lost (OQ-138).
     */
    fun onStart() {
        val deadline = clock.instant() + maxOf(lostAfter, startGrace)
        for (agent in deadlines.holders()) {
            locked(agent.id) { deadlines.restart(agent.tenantId, agent.id, deadline) }
        }
    }

    /** Marks steps whose deadline passed, retries queued steps of online agents, refreshes the metric. */
    fun tick() {
        for (step in deadlines.overdue(clock.instant())) {
            if (ledger.lost(step.tenantId, step.id)) log.warn("step {} lost: its agent did not report it", step.id)
        }
        for (step in deadlines.silent(clock.instant(), lostAfter)) {
            silent.timeOut(step) { agent, work -> locked(agent, work) }
        }
        for (agent in links.connected()) {
            if (links.online(agent.agentId)) serve(agent.tenantId, agent.agentId)
        }
        metrics.waiting(waiting())
    }

    /** Sends the agent's queued steps, then the CancelStep of its cancelled ones that have none yet. */
    private fun serve(
        tenantId: UUID,
        agentId: UUID,
    ) = locked(agentId) {
        val active = ledger.active(tenantId, agentId)
        val queued = active.filter { it.status == StepState.QUEUED }
        deliver(ConnectedAgent(agentId, tenantId, ""), queued, sentBefore = null)
        cancels.send(agentId, active)
    }

    /**
     * Sends [steps] in order and stops at the first one the session refuses. A step the agent's role does not
     * allow is never sent (V1, defence in depth: sources and runs do not create one): it fails and the
     * round goes on.
     */
    private fun deliver(
        agent: ConnectedAgent,
        steps: List<StepView>,
        sentBefore: Instant?,
    ) {
        if (steps.isEmpty()) return
        val role = guard.roleOf(agent)
        for (step in steps) {
            if (!role.allows(step.action)) {
                guard.refuse(agent, role, step)
            } else if (!send(agent, step, sentBefore)) {
                return
            }
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
        val queued = result == SendResult.Queued
        if (!queued) {
            ledger.release(agent.tenantId, step.id)
            metrics.refused()
            log.info("step {} back in the queue of agent {}: {}", step.id, agent.agentId, result)
        }
        return queued
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

/** A step the agent's role does not allow is never sent (V1, defence in depth): it fails, and the log says why. */
private class RoleGuard(
    private val roles: RoleLedger,
) {
    fun roleOf(agent: ConnectedAgent): AgentRole = roles.roleOf(agent.tenantId, agent.agentId)

    fun refuse(
        agent: ConnectedAgent,
        role: AgentRole,
        step: StepView,
    ) {
        val message = "agent role mismatch: a ${role.stored} does not run ${step.action.stored}"
        if (!roles.refuse(agent.tenantId, step.id, message)) return
        log.warn(
            "agent role mismatch: step {} of agent {} not sent, role {} does not allow action {}",
            step.id,
            agent.agentId,
            role.stored,
            step.action.stored,
        )
    }
}
