// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents.dispatch

import dev.sard.server.agents.stream.SendResult
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

/** What the dispatcher does for a run its person cancelled (J1a): CancelStep now, on a tick, or on the Hello. */
@MutFlowTest
class StepDispatcherCancelTest {
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

    private val sentAt = DISPATCH_NOW - Duration.ofSeconds(5)

    @Test
    fun `a cancel reaches an online agent at once, for a dispatched and a running step`() {
        online()
        val ledger =
            ledger(
                step(1, StepState.RUNNING, cancelRequested = true),
                step(2, StepState.DISPATCHED, sentAt, cancelRequested = true),
                step(3, StepState.RUNNING),
            )

        MutFlow.underTest { dispatcher(ledger).onCancelled(TENANT, AGENT.agentId) }

        assertEquals(listOf(1L, 2L), links.cancelledIds())
        assertEquals(emptyList(), links.sentIds())
    }

    @Test
    fun `a cancel of an offline agent sends nothing, the Hello will`() {
        val ledger = ledger(step(1, StepState.RUNNING, cancelRequested = true))

        MutFlow.underTest { dispatcher(ledger).onCancelled(TENANT, AGENT.agentId) }

        assertEquals(emptyList(), links.cancelledIds())
    }

    @Test
    fun `a cancel the session could not queue goes out on the next tick, and only once`() {
        online()
        links.cancelAnswers += SendResult.QueueFull
        val ledger = ledger(step(1, StepState.RUNNING, cancelRequested = true))
        val dispatcher = dispatcher(ledger)

        MutFlow.underTest { dispatcher.onCancelled(TENANT, AGENT.agentId) }
        assertEquals(emptyList(), links.cancelledIds())

        MutFlow.underTest { dispatcher.tick() }
        MutFlow.underTest { dispatcher.tick() }

        assertEquals(listOf(1L), links.cancelledIds())
    }

    @Test
    fun `a session that ends forgets what it was sent, so the next one gets the cancel again`() {
        online()
        val ledger = ledger(step(1, StepState.RUNNING, cancelRequested = true))
        val dispatcher = dispatcher(ledger)
        dispatcher.onCancelled(TENANT, AGENT.agentId)

        MutFlow.underTest { dispatcher.onDisconnected(AGENT, "STREAM_ENDED") }
        dispatcher.tick()

        assertEquals(listOf(1L, 1L), links.cancelledIds())
    }

    @Test
    fun `a Hello that lists a cancelled step gets its CancelStep and takes the deadline off it`() {
        val ledger = ledger(step(1, StepState.RUNNING, cancelRequested = true))
        ledger.deadlines[UUID(0, 1)] = DISPATCH_NOW + LOST_AFTER

        MutFlow.underTest { dispatcher(ledger).onHello(AGENT, listOf(UUID(0, 1).toString())) }

        assertEquals(listOf(1L), links.cancelledIds())
        assertEquals(null, ledger.deadline(1))
    }

    @Test
    fun `a Hello that does not list a dispatched cancelled step gets its CancelStep, not the step`() {
        val ledger = ledger(step(1, StepState.DISPATCHED, sentAt, cancelRequested = true))

        MutFlow.underTest { dispatcher(ledger).onHello(AGENT, emptyList()) }

        assertEquals(listOf(1L), links.cancelledIds())
        assertEquals(emptyList(), links.sentIds())
        assertEquals(DISPATCH_NOW + LOST_AFTER, ledger.deadline(1))
        assertEquals(0, metrics.redispatched)
    }

    @Test
    fun `a Hello that does not list a running cancelled step only starts its window`() {
        val ledger = ledger(step(1, StepState.RUNNING, cancelRequested = true))

        MutFlow.underTest { dispatcher(ledger).onHello(AGENT, emptyList()) }

        assertEquals(emptyList(), links.cancelledIds())
        assertEquals(DISPATCH_NOW + LOST_AFTER, ledger.deadline(1))
    }

    @Test
    fun `the Hello of an agent still holding queued steps delivers them after the cancels`() {
        val ledger = ledger(step(1, StepState.RUNNING, cancelRequested = true), step(2))

        MutFlow.underTest { dispatcher(ledger).onHello(AGENT, listOf(UUID(0, 1).toString())) }

        assertEquals(listOf(1L), links.cancelledIds())
        assertEquals(listOf(2L), links.sentIds())
    }

    @Test
    fun `a failure while sending a cancel is logged, never thrown at the caller`() {
        online()
        val ledger = ledger(step(1, StepState.RUNNING, cancelRequested = true))
        val failing =
            object : AgentLinks by links {
                override fun cancel(
                    agentId: UUID,
                    commandId: String,
                ): SendResult = error("session gone")
            }
        val dispatcher =
            StepDispatcher(ledger, ledger, failing, clock, LOST_AFTER, metrics, ledger, Duration.ZERO) {
                WaitingSteps(0, 0)
            }

        val logged = dispatchLog { MutFlow.underTest { dispatcher.onCancelled(TENANT, AGENT.agentId) } }

        assertEquals(1, logged.count { "waits for the next check" in it && "session gone" in it })
    }
}
