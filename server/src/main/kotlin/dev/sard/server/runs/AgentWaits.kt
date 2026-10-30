// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.runs

import dev.sard.server.persistence.TenantSessions
import org.slf4j.LoggerFactory
import java.time.Clock
import java.time.Duration
import java.time.Instant

private val log = LoggerFactory.getLogger(AgentWaits::class.java)

/** The message of a run that was not run: its agent stayed offline until the wait deadline (J1c). */
const val AGENT_UNAVAILABLE_MESSAGE = "the agent was not connected until the wait deadline"

// Native SQL names tenant_id explicitly (ADR 0013, rule 8); every WHERE names the status it expects.
private const val CLOSE_STEP =
    "update run_steps set status = 'agent_unavailable', message = '$AGENT_UNAVAILABLE_MESSAGE', finished_at = :now " +
        "where tenant_id = :tenant and id = :step and status = 'queued' and exists (select 1 from runs r " +
        "where r.tenant_id = run_steps.tenant_id and r.id = run_steps.run_id and r.status = 'queued' " +
        "and r.agent_wait_until <= :now)"
private const val CLOSE_RUN =
    "update runs set status = 'agent_unavailable', message = '$AGENT_UNAVAILABLE_MESSAGE', finished_at = :now, " +
        "agent_unavailable_since = " +
        "(select a.last_seen_at from agents a where a.tenant_id = :tenant and a.id = :agent) " +
        "where tenant_id = :tenant and status = 'queued' " +
        "and id = (select run_id from run_steps where tenant_id = :tenant and id = :step)"

/**
 * Closes the runs whose agent stayed offline until the wait deadline (J1c): the run and its step become
 * `agent_unavailable` (never sent, never run), the source is free again and [RunFinished] is announced. Only a queued
 * step of an agent that is offline now expires (Р-J11): a queued step of an online agent waits for the dispatcher.
 *
 * After a server start the agents have not had the chance to reconnect: for [grace] after [startedAt] the wait of an
 * agent not seen since the start does not expire (J1d, ДJ9). An agent seen since the start and gone again has none.
 * Every move is one guarded update, so two ticks, or two servers, close a run once.
 */
class AgentWaits(
    private val deadlines: StepDeadlines,
    private val sessions: TenantSessions,
    private val clock: Clock,
    private val announcer: RunAnnouncer,
    private val presence: AgentOnline,
    private val startedAt: Instant,
    private val grace: Duration,
) {
    /** Closes every run whose wait is over and whose agent is offline; the number closed. Never throws. */
    fun expire(): Int {
        val now = clock.instant()
        val over = runCatching { deadlines.waitsOver(now) }
        over.onFailure { log.warn("waits not checked: {}", it.toString()) }
        val offline = over.getOrDefault(emptyList()).filterNot { presence.online(it.agentId) || settling(it, now) }
        return offline.count { expired(it, now) }
    }

    /** The agent has not been seen since the server started, and the start is recent. */
    private fun settling(
        step: WaitOver,
        now: Instant,
    ): Boolean = (step.lastSeenAt?.isBefore(startedAt) ?: true) && now.isBefore(startedAt + grace)

    /** The run is final already: a failing announcement is only the late wake-up of its notification. */
    private fun announce(step: WaitOver) {
        runCatching { announcer.announce(step.tenantId, step.stepId) }
            .onFailure { log.warn("run of step {} not announced: {}", step.stepId, it.toString()) }
    }

    private fun expired(
        step: WaitOver,
        now: Instant,
    ): Boolean {
        val moved =
            runCatching {
                sessions.inTenant(step.tenantId) { session ->
                    val values = mapOf("tenant" to step.tenantId, "step" to step.stepId, "agent" to step.agentId)
                    val closed = session.execute(CLOSE_STEP, values + ("now" to now)) == 1
                    if (closed) {
                        val watch = announcer.events.watchStep(session, step.tenantId, step.stepId)
                        session.execute(CLOSE_RUN, values + ("now" to now))
                        announcer.events.settled(session, listOfNotNull(watch))
                    }
                    closed
                }
            }.onFailure { log.warn("wait of step {} not closed: {}", step.stepId, it.toString()) }
                .getOrDefault(false)
        if (moved) announce(step)
        return moved
    }
}
