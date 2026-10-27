// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents.dispatch

import dev.sard.server.pki.MovableClock
import dev.sard.server.runs.StepState
import dev.sard.server.runs.StepView
import dev.sard.server.runs.WaitingSteps
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import java.time.Duration
import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertEquals

/** What the dispatcher does about a running step whose agent stays silent past the timeout and the window (J1b). */
@MutFlowTest
class StepDispatcherTimeoutTest {
    private val clock = MovableClock(DISPATCH_NOW)
    private val links = FakeLinks()
    private val metrics = RecordingDispatchMetrics()

    private fun dispatcher(ledger: FakeLedger) =
        StepDispatcher(ledger, ledger, links, clock, LOST_AFTER, metrics, ledger, Duration.ZERO) { WaitingSteps(0, 0) }

    private fun ledger(vararg steps: StepView) = FakeLedger(*steps, now = { clock.now })

    private fun online() {
        links.online += AGENT.agentId
        links.sessions += AGENT
    }

    // Сценарий: Агент на связи молчит после срока и окна — шаг закрывает сервер
    @Test
    fun `a tick times out a silent step with the lost window and sends its online agent a CancelStep`() {
        online()
        val ledger = ledger(step(1, StepState.RUNNING))
        ledger.silent += UUID(0, 1)

        MutFlow.underTest { dispatcher(ledger).tick() }

        assertEquals(StepState.TIMED_OUT, ledger.status(1))
        assertEquals(listOf(LOST_AFTER), ledger.windows)
        assertEquals(listOf(1L), links.cancelledIds())
    }

    @Test
    fun `an agent that is offline gets no CancelStep, its Hello finds the step closed`() {
        val ledger = ledger(step(1, StepState.RUNNING))
        ledger.silent += UUID(0, 1)

        MutFlow.underTest { dispatcher(ledger).tick() }

        assertEquals(StepState.TIMED_OUT, ledger.status(1))
        assertEquals(emptyList(), links.cancelledIds())
    }

    @Test
    fun `a step that a result closed before the tick is not cancelled`() {
        online()
        val ledger = ledger(step(1, StepState.SUCCEEDED))
        ledger.silent += UUID(0, 1)

        MutFlow.underTest { dispatcher(ledger).tick() }

        assertEquals(StepState.SUCCEEDED, ledger.status(1))
        assertEquals(emptyList(), links.cancelledIds())
    }

    @Test
    fun `a failure while cancelling a timed out step is logged, the tick goes on`() {
        online()
        val ledger = ledger(step(1, StepState.RUNNING), step(2, StepState.RUNNING))
        ledger.silent += setOf(UUID(0, 1), UUID(0, 2))
        val failing =
            object : AgentLinks by links {
                override fun cancel(
                    agentId: UUID,
                    commandId: String,
                ): dev.sard.server.agents.stream.SendResult = error("session gone")
            }
        val dispatcher =
            StepDispatcher(ledger, ledger, failing, clock, LOST_AFTER, metrics, ledger, Duration.ZERO) {
                WaitingSteps(0, 0)
            }

        val logged = dispatchLog { MutFlow.underTest { dispatcher.tick() } }

        assertEquals(listOf(StepState.TIMED_OUT, StepState.TIMED_OUT), listOf(ledger.status(1), ledger.status(2)))
        assertEquals(2, logged.count { "session gone" in it })
    }
}
