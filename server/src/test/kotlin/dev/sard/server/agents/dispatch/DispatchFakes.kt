// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents.dispatch

import dev.sard.proto.agent.v1.RunStep
import dev.sard.server.agents.stream.ConnectedAgent
import dev.sard.server.agents.stream.SendResult
import dev.sard.server.runs.Action
import dev.sard.server.runs.DispatchLedger
import dev.sard.server.runs.InTenant
import dev.sard.server.runs.LostDeadlines
import dev.sard.server.runs.StepState
import dev.sard.server.runs.StepView
import dev.sard.server.runs.WaitingSteps
import java.time.Duration
import java.time.Instant
import java.util.UUID

internal val DISPATCH_NOW: Instant = Instant.parse("2026-09-30T12:00:00Z")
internal val LOST_AFTER: Duration = Duration.ofSeconds(60)
internal val TENANT: UUID = UUID(1, 1)
internal val AGENT = ConnectedAgent(UUID(2, 2), TENANT, "serial")

internal fun step(
    n: Long,
    status: StepState = StepState.QUEUED,
    dispatchedAt: Instant? = null,
    agent: UUID = AGENT.agentId,
) = StepView(
    id = UUID(0, n),
    ordinal = 0,
    action = Action.BACKUP,
    status = status,
    agentId = agent,
    sourceId = UUID(9, n),
    plugin = "postgresql",
    repositoryName = "main",
    config = """{"n": $n}""",
    queuedAt = DISPATCH_NOW,
    dispatchedAt = dispatchedAt,
)

/**
 * The guarded transitions of StepTransitions over a list kept in creation order, with the lost
 * deadline of each step (FXs: `run_steps.lost_deadline`) and the system reads of StepDeadlines.
 */
internal class FakeLedger(
    vararg steps: StepView,
    private val now: () -> Instant = { DISPATCH_NOW },
) : DispatchLedger,
    LostDeadlines {
    val steps = steps.toMutableList()
    val calls = mutableListOf<String>()
    val deadlines = mutableMapOf<UUID, Instant>()

    fun deadline(n: Long) = deadlines[UUID(0, n)]

    fun status(n: Long) = steps.single { it.id == UUID(0, n) }.status

    private fun move(
        stepId: UUID,
        from: Set<StepState>,
        change: (StepView) -> StepView,
    ): Boolean {
        val i = steps.indexOfFirst { it.id == stepId }
        if (i < 0 || steps[i].status !in from) return false
        steps[i] = change(steps[i])
        return true
    }

    override fun active(
        tenantId: UUID,
        agentId: UUID,
    ) = steps.filter { tenantId == TENANT && it.agentId == agentId && it.status.active }

    override fun claim(
        tenantId: UUID,
        stepId: UUID,
    ) = move(stepId, setOf(StepState.QUEUED)) { it.copy(status = StepState.DISPATCHED, dispatchedAt = DISPATCH_NOW) }
        .also { if (it) deadlines.remove(stepId) }
        .also { calls += "claim ${stepId.leastSignificantBits}" }

    override fun release(
        tenantId: UUID,
        stepId: UUID,
    ) = move(stepId, setOf(StepState.DISPATCHED)) { it.copy(status = StepState.QUEUED, dispatchedAt = null) }
        .also { if (it) deadlines.remove(stepId) }
        .also { calls += "release ${stepId.leastSignificantBits}" }

    override fun redispatch(
        tenantId: UUID,
        stepId: UUID,
        sentBefore: Instant,
    ): Boolean {
        val sent = steps.single { it.id == stepId }.dispatchedAt
        val moved = sent != null && sent < sentBefore && move(stepId, setOf(StepState.DISPATCHED)) { it }
        if (moved) deadlines.remove(stepId)
        calls += "redispatch ${stepId.leastSignificantBits} $moved"
        return moved
    }

    override fun lost(
        tenantId: UUID,
        stepId: UUID,
    ): Boolean {
        val due = deadlines[stepId]?.let { !now().isBefore(it) } == true
        return (due && move(stepId, IN_FLIGHT) { it.copy(status = StepState.LOST) })
            .also { calls += "lost ${stepId.leastSignificantBits} $it" }
    }

    private fun inFlight(agentId: UUID) = steps.filter { it.agentId == agentId && it.status in IN_FLIGHT }.map { it.id }

    override fun expectAll(
        tenantId: UUID,
        agentId: UUID,
        deadline: Instant,
    ) = earliest(inFlight(agentId), deadline)

    override fun expect(
        tenantId: UUID,
        agentId: UUID,
        stepIds: Collection<UUID>,
        deadline: Instant,
    ) = earliest(inFlight(agentId).filter { it in stepIds }, deadline)

    override fun confirm(
        tenantId: UUID,
        agentId: UUID,
        stepIds: Collection<UUID>,
    ) = inFlight(agentId).filter { it in stepIds && deadlines.remove(it) != null }.size

    override fun restart(
        tenantId: UUID,
        agentId: UUID,
        deadline: Instant,
    ) = inFlight(agentId).onEach { deadlines[it] = deadline }.size

    private fun earliest(
        ids: List<UUID>,
        deadline: Instant,
    ) = ids.onEach { id -> deadlines.merge(id, deadline) { old, new -> minOf(old, new) } }.size

    override fun overdue(now: Instant) =
        steps
            .filter { it.status in IN_FLIGHT && deadlines[it.id]?.let { d -> !now.isBefore(d) } == true }
            .map { InTenant(TENANT, it.id) }

    override fun holders() = steps.filter { it.status in IN_FLIGHT }.map { InTenant(TENANT, it.agentId) }.distinct()
}

private val IN_FLIGHT = setOf(StepState.DISPATCHED, StepState.RUNNING)

/** Sessions as the dispatcher sees them: who is online, what each send returns. */
internal class FakeLinks : AgentLinks {
    val online = mutableSetOf<UUID>()
    val sessions = mutableListOf<ConnectedAgent>()
    val sent = mutableListOf<RunStep>()
    val answers = ArrayDeque<SendResult>()

    override fun connected() = sessions.toList()

    override fun online(agentId: UUID) = agentId in online

    override fun send(
        agentId: UUID,
        step: RunStep,
    ): SendResult {
        val answer = answers.removeFirstOrNull() ?: SendResult.Queued
        if (answer == SendResult.Queued) sent += step
        return answer
    }

    fun sentIds() = sent.map { UUID.fromString(it.commandId).leastSignificantBits }
}

/** What the dispatcher logs while [block] runs, as formatted messages. */
internal fun dispatchLog(block: () -> Unit): List<String> {
    val logger = org.slf4j.LoggerFactory.getLogger(StepDispatcher::class.java) as ch.qos.logback.classic.Logger
    val appender =
        ch.qos.logback.core.read
            .ListAppender<ch.qos.logback.classic.spi.ILoggingEvent>()
    appender.start()
    logger.addAppender(appender)
    try {
        block()
    } finally {
        logger.detachAppender(appender)
        appender.stop()
    }
    return appender.list.map { it.formattedMessage }
}

internal class RecordingDispatchMetrics : DispatchMetrics {
    var redispatched = 0
    var refused = 0
    var waiting: WaitingSteps? = null

    override fun redispatched() {
        redispatched++
    }

    override fun refused() {
        refused++
    }

    override fun waiting(steps: WaitingSteps) {
        waiting = steps
    }
}
