// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents.results

import ch.qos.logback.classic.Level
import ch.qos.logback.classic.Logger
import ch.qos.logback.classic.spi.ILoggingEvent
import ch.qos.logback.core.read.ListAppender
import dev.sard.proto.agent.v1.StepResult
import dev.sard.proto.agent.v1.StepStatus
import dev.sard.server.agents.stream.ConnectedAgent
import dev.sard.server.agents.stream.SendResult
import dev.sard.server.runs.Recorded
import dev.sard.server.runs.StepReport
import dev.sard.server.runs.StepState
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import org.slf4j.LoggerFactory
import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith

private val AGENT = ConnectedAgent(UUID.randomUUID(), UUID.randomUUID(), "01")
private val STEP: UUID = UUID.randomUUID()

/** Calls in the order the receiver made them. */
private class Calls {
    val list = mutableListOf<String>()
}

private class FakeRecorder(
    private val calls: Calls,
    var answer: () -> Recorded = { Recorded.Closed(StepState.SUCCEEDED, null) },
) : ResultRecorder {
    val reports = mutableListOf<Triple<UUID, UUID, StepReport>>()

    override fun record(
        tenantId: UUID,
        agentId: UUID,
        stepId: UUID,
        report: StepReport,
    ): Recorded {
        calls.list += "record $stepId"
        reports += Triple(tenantId, agentId, report)
        return answer()
    }
}

private class FakeAcks(
    private val calls: Calls,
    var answer: SendResult = SendResult.Queued,
) : ResultAcks {
    override fun ack(
        agentId: UUID,
        commandId: String,
    ): SendResult {
        calls.list += "ack $agentId $commandId"
        return answer
    }
}

private class RecordingResultMetrics : ResultMetrics {
    val outcomes = mutableListOf<String>()

    override fun recorded(outcome: String) {
        outcomes += outcome
    }
}

/** A StepResult is recorded, then acknowledged; never acknowledged when recording failed (S7a). */
@MutFlowTest
class StepResultReceiverTest {
    private val calls = Calls()
    private val recorder = FakeRecorder(calls)
    private val acks = FakeAcks(calls)
    private val metrics = RecordingResultMetrics()
    private val receiver = StepResultReceiver(recorder, acks, metrics)

    private fun result(
        commandId: String = STEP.toString(),
        status: StepStatus = StepStatus.STEP_STATUS_SUCCEEDED,
    ) = StepResult
        .newBuilder()
        .setCommandId(commandId)
        .setStatus(status)
        .build()

    private fun securityLog(block: () -> Unit) = logOf(SECURITY_LOG, block)

    /** What the receiver's own logger writes, debug included. */
    private fun receiverLog(block: () -> Unit): List<String> {
        val events = logOf(StepResultReceiver::class.java.name, block)
        return events.map { it.formattedMessage }
    }

    private fun logOf(
        name: String,
        block: () -> Unit,
    ): List<ILoggingEvent> {
        val logger = LoggerFactory.getLogger(name) as Logger
        val level = logger.level
        val appender = ListAppender<ILoggingEvent>()
        appender.start()
        logger.addAppender(appender)
        logger.level = Level.DEBUG
        try {
            block()
        } finally {
            logger.level = level
            logger.detachAppender(appender)
            appender.stop()
        }
        return appender.list
    }

    @Test
    fun `a result is recorded in the agent's tenant, then acknowledged`() {
        MutFlow.underTest { receiver.handle(AGENT, result()) }

        assertEquals(listOf("record $STEP", "ack ${AGENT.agentId} $STEP"), calls.list)
        val (tenant, agent, report) = recorder.reports.single()
        assertEquals(listOf(AGENT.tenantId, AGENT.agentId), listOf(tenant, agent))
        assertEquals(StepReport(StepState.SUCCEEDED, "", null), report)
        assertEquals(listOf("succeeded"), metrics.outcomes)
    }

    @Test
    fun `a failed recording is not acknowledged, so the agent sends the result again`() {
        recorder.answer = { throw IllegalStateException("database down") }

        assertFailsWith<IllegalStateException> { MutFlow.underTest { receiver.handle(AGENT, result()) } }

        assertEquals(listOf("record $STEP"), calls.list)
        assertEquals(emptyList(), metrics.outcomes)
    }

    @Test
    fun `every recorded outcome is acknowledged and counted`() {
        val outcomes =
            mapOf(
                Recorded.Closed(StepState.REJECTED, null) to "rejected",
                Recorded.Closed(StepState.FAILED, "no final status") to "invalid",
                Recorded.Repeated to "repeated",
                Recorded.Late(kept = true) to "late",
                Recorded.Late(kept = false) to "late",
                Recorded.NotDispatched to "not_dispatched",
                Recorded.Unknown to "unknown",
            )
        for ((recorded, outcome) in outcomes) {
            recorder.answer = { recorded }
            calls.list.clear()
            metrics.outcomes.clear()

            MutFlow.underTest { receiver.handle(AGENT, result()) }

            assertEquals("ack ${AGENT.agentId} $STEP", calls.list.last())
            assertEquals(listOf(outcome), metrics.outcomes)
        }
    }

    @Test
    fun `a result for no step of this agent is acknowledged and logged for security`() {
        recorder.answer = { Recorded.Unknown }

        val events = securityLog { MutFlow.underTest { receiver.handle(AGENT, result()) } }

        val event = events.single()
        assertEquals(Level.WARN, event.level)
        assertEquals(
            "agent ${AGENT.agentId} (tenant ${AGENT.tenantId}) sent a result for command $STEP, " +
                "which is no step of it; acknowledged, nothing recorded",
            event.formattedMessage,
        )
    }

    @Test
    fun `a command id that is no UUID is acknowledged unread and logged without its text`() {
        val hostile = "x\nFAKE LOG LINE"

        val events = securityLog { MutFlow.underTest { receiver.handle(AGENT, result(commandId = hostile)) } }

        assertEquals(listOf("ack ${AGENT.agentId} $hostile"), calls.list)
        assertEquals(
            "agent ${AGENT.agentId} (tenant ${AGENT.tenantId}) sent a result for a command id that is " +
                "no UUID (15 characters); acknowledged, nothing recorded",
            events.single().formattedMessage,
        )
        assertEquals(listOf("unknown"), metrics.outcomes)
    }

    @Test
    fun `other outcomes leave the security log alone`() {
        for (recorded in listOf(Recorded.Repeated, Recorded.Late(kept = true), Recorded.NotDispatched)) {
            recorder.answer = { recorded }

            val events = securityLog { MutFlow.underTest { receiver.handle(AGENT, result()) } }

            assertEquals(emptyList(), events)
        }
    }

    @Test
    fun `a late result is logged with whether its output was kept`() {
        for (kept in listOf(true, false)) {
            recorder.answer = { Recorded.Late(kept) }

            val lines = receiverLog { MutFlow.underTest { receiver.handle(AGENT, result()) } }

            assertEquals(listOf("result for lost step $STEP; it stays lost (output kept: $kept)"), lines)
        }
    }

    @Test
    fun `an acknowledgement the session queued leaves no note, a refused one does`() {
        val queued = receiverLog { MutFlow.underTest { receiver.handle(AGENT, result()) } }
        acks.answer = SendResult.QueueFull
        val refused = receiverLog { MutFlow.underTest { receiver.handle(AGENT, result()) } }

        assertEquals(emptyList(), queued)
        val note = "ResultAck for $STEP not queued (QueueFull); the agent resends on its next stream"
        assertEquals(listOf(note), refused)
    }

    @Test
    fun `an acknowledgement the session refused is not retried, the agent resends on its next stream`() {
        acks.answer = SendResult.NotConnected

        MutFlow.underTest { receiver.handle(AGENT, result()) }

        assertEquals(1, calls.list.count { it.startsWith("ack") })
        assertEquals(listOf("succeeded"), metrics.outcomes)
    }
}
