// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents.dispatch

import dev.sard.proto.agent.v1.RunStep
import dev.sard.server.agents.stream.ConnectedAgent
import dev.sard.server.agents.stream.SendResult
import dev.sard.server.runs.Action
import dev.sard.server.runs.DispatchLedger
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

/** The guarded transitions of StepTransitions over a list kept in creation order. */
internal class FakeLedger(
    vararg steps: StepView,
) : DispatchLedger {
    val steps = steps.toMutableList()
    val calls = mutableListOf<String>()

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
        .also { calls += "claim ${stepId.leastSignificantBits}" }

    override fun release(
        tenantId: UUID,
        stepId: UUID,
    ) = move(stepId, setOf(StepState.DISPATCHED)) { it.copy(status = StepState.QUEUED, dispatchedAt = null) }
        .also { calls += "release ${stepId.leastSignificantBits}" }

    override fun redispatch(
        tenantId: UUID,
        stepId: UUID,
        sentBefore: Instant,
    ): Boolean {
        val sent = steps.single { it.id == stepId }.dispatchedAt
        val moved = sent != null && sent < sentBefore && move(stepId, setOf(StepState.DISPATCHED)) { it }
        calls += "redispatch ${stepId.leastSignificantBits} $moved"
        return moved
    }

    override fun lost(
        tenantId: UUID,
        stepId: UUID,
    ) = move(stepId, setOf(StepState.RUNNING)) { it.copy(status = StepState.LOST) }
        .also { calls += "lost ${stepId.leastSignificantBits} $it" }
}

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
