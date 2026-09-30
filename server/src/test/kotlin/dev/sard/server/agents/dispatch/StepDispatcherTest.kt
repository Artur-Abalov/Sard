// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents.dispatch

import dev.sard.server.agents.stream.SendResult
import dev.sard.server.pki.MovableClock
import dev.sard.server.runs.StepState
import dev.sard.server.runs.WaitingSteps
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import java.time.Duration
import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertEquals

/** The dispatch rules of the S6a ADR draft, against fake transitions and sessions. */
@MutFlowTest
class StepDispatcherTest {
    private val clock = MovableClock(DISPATCH_NOW)
    private val links = FakeLinks()
    private val metrics = RecordingDispatchMetrics()

    private fun dispatcher(
        ledger: FakeLedger,
        waiting: WaitingSteps = WaitingSteps(0, 0),
    ) = StepDispatcher(ledger, links, clock, LOST_AFTER, metrics) { waiting }

    private fun online() {
        links.online += AGENT.agentId
        links.sessions += AGENT
    }

    @Test
    fun `a queued step goes to an online agent and is dispatched`() {
        online()
        val ledger = FakeLedger(step(1))

        MutFlow.underTest { dispatcher(ledger).onQueued(TENANT, AGENT.agentId) }

        assertEquals(listOf(1L), links.sentIds())
        assertEquals(StepState.DISPATCHED, ledger.status(1))
    }

    @Test
    fun `an offline agent gets nothing, its step stays queued`() {
        val ledger = FakeLedger(step(1))
        MutFlow.underTest { dispatcher(ledger).onQueued(TENANT, AGENT.agentId) }
        assertEquals(emptyList(), links.sentIds())
        assertEquals(StepState.QUEUED, ledger.status(1))
    }

    @Test
    fun `a refused send puts the step back in the queue and stops the round`() {
        for (refusal in listOf(SendResult.QueueFull, SendResult.NotConnected)) {
            online()
            links.answers += refusal
            val ledger = FakeLedger(step(1), step(2))

            MutFlow.underTest { dispatcher(ledger).onQueued(TENANT, AGENT.agentId) }

            val statuses = listOf(ledger.status(1), ledger.status(2))
            assertEquals(listOf(StepState.QUEUED, StepState.QUEUED), statuses, "$refusal")
            assertEquals(listOf("claim 1", "release 1"), ledger.calls, "$refusal")
        }
        assertEquals(2, metrics.refused)
    }

    @Test
    fun `a step another path claimed first is not sent twice`() {
        online()
        val ledger = FakeLedger(step(1), step(2))
        ledger.claim(TENANT, UUID(0, 1))

        MutFlow.underTest { dispatcher(ledger).onQueued(TENANT, AGENT.agentId) }

        assertEquals(listOf(2L), links.sentIds())
    }

    @Test
    fun `a Hello delivers queued steps in creation order`() {
        val ledger = FakeLedger(step(1), step(2), step(3))
        MutFlow.underTest { dispatcher(ledger).onHello(AGENT, emptyList()) }
        assertEquals(listOf(1L, 2L, 3L), links.sentIds())
    }

    @Test
    fun `steps the Hello lists are never sent again`() {
        val earlier = DISPATCH_NOW - Duration.ofSeconds(5)
        val listed = listOf(step(1, StepState.DISPATCHED, earlier), step(2, StepState.RUNNING))
        val ledger = FakeLedger(*listed.toTypedArray())
        val dispatcher = dispatcher(ledger)

        MutFlow.underTest { dispatcher.onHello(AGENT, listed.map { it.id.toString() }) }
        clock.now += LOST_AFTER
        MutFlow.underTest { dispatcher.tick() }

        assertEquals(emptyList(), links.sentIds())
        assertEquals(listOf(StepState.DISPATCHED, StepState.RUNNING), listOf(ledger.status(1), ledger.status(2)))
    }

    @Test
    fun `a dispatched step the Hello does not list is sent again and counted`() {
        val ledger = FakeLedger(step(1, StepState.DISPATCHED, DISPATCH_NOW - Duration.ofSeconds(5)))
        MutFlow.underTest { dispatcher(ledger).onHello(AGENT, emptyList()) }
        assertEquals(listOf(1L), links.sentIds())
        assertEquals(1, metrics.redispatched)
    }

    @Test
    fun `a dispatched step already sent to this session is not sent again`() {
        val ledger = FakeLedger(step(1, StepState.DISPATCHED, DISPATCH_NOW))
        MutFlow.underTest { dispatcher(ledger).onHello(AGENT, emptyList()) }
        assertEquals(emptyList(), links.sentIds())
        assertEquals(0, metrics.redispatched)
    }

    @Test
    fun `a refused resend goes back to the queue too`() {
        links.answers += SendResult.QueueFull
        val ledger = FakeLedger(step(1, StepState.DISPATCHED, DISPATCH_NOW - Duration.ofSeconds(5)), step(2))

        MutFlow.underTest { dispatcher(ledger).onHello(AGENT, emptyList()) }

        assertEquals(listOf(StepState.QUEUED, StepState.QUEUED), listOf(ledger.status(1), ledger.status(2)))
    }

    @Test
    fun `a running step the Hello does not list is lost only once the window has passed`() {
        val ledger = FakeLedger(step(1, StepState.RUNNING))
        val dispatcher = dispatcher(ledger)
        MutFlow.underTest { dispatcher.onHello(AGENT, emptyList()) }

        clock.now = DISPATCH_NOW + LOST_AFTER - Duration.ofMillis(1)
        MutFlow.underTest { dispatcher.tick() }
        assertEquals(StepState.RUNNING, ledger.status(1))

        clock.now = DISPATCH_NOW + LOST_AFTER
        val log = dispatchLog { MutFlow.underTest { dispatcher.tick() } }
        assertEquals(StepState.LOST, ledger.status(1))
        assertEquals(listOf("step ${UUID(0, 1)} lost: its agent did not report it"), log)
        assertEquals(emptyList(), links.sentIds(), "a lost step is never sent again")
    }

    @Test
    fun `a later Hello that lists the step takes it off the watch`() {
        val ledger = FakeLedger(step(1, StepState.RUNNING))
        val dispatcher = dispatcher(ledger)
        MutFlow.underTest { dispatcher.onHello(AGENT, emptyList()) }
        MutFlow.underTest { dispatcher.onHello(AGENT, listOf(UUID(0, 1).toString())) }

        clock.now += LOST_AFTER
        MutFlow.underTest { dispatcher.tick() }

        assertEquals(StepState.RUNNING, ledger.status(1))
    }

    @Test
    fun `a step the window marks once is not marked again`() {
        val ledger = FakeLedger(step(1, StepState.RUNNING))
        val dispatcher = dispatcher(ledger)
        MutFlow.underTest { dispatcher.onHello(AGENT, emptyList()) }
        clock.now += LOST_AFTER
        MutFlow.underTest { dispatcher.tick() }
        MutFlow.underTest { dispatcher.tick() }
        assertEquals(listOf("lost 1 true"), ledger.calls)
    }

    @Test
    fun `a step a result closed before the window passed is not reported lost`() {
        val ledger = FakeLedger(step(1, StepState.RUNNING))
        val dispatcher = dispatcher(ledger)
        MutFlow.underTest { dispatcher.onHello(AGENT, emptyList()) }
        ledger.steps[0] = ledger.steps[0].copy(status = StepState.SUCCEEDED)
        clock.now += LOST_AFTER

        val log = dispatchLog { MutFlow.underTest { dispatcher.tick() } }

        assertEquals(listOf("lost 1 false"), ledger.calls)
        assertEquals(emptyList(), log)
    }

    @Test
    fun `the tick delivers queued steps of online sessions only`() {
        val offline = step(2, agent = UUID(3, 3))
        val ledger = FakeLedger(step(1), offline)
        online()
        links.sessions += AGENT.copy(agentId = offline.agentId)

        MutFlow.underTest { dispatcher(ledger).tick() }

        assertEquals(listOf(1L), links.sentIds())
    }

    @Test
    fun `the tick hands the waiting counts to the metrics`() {
        MutFlow.underTest { dispatcher(FakeLedger(), WaitingSteps(3, 2)).tick() }
        assertEquals(WaitingSteps(3, 2), metrics.waiting)
    }

    @Test
    fun `a failure while queuing is logged, never thrown at the caller whose run is committed`() {
        online()
        val broken =
            object : dev.sard.server.runs.DispatchLedger by FakeLedger() {
                override fun active(
                    tenantId: UUID,
                    agentId: UUID,
                ) = error("database down")
            }
        val dispatcher = StepDispatcher(broken, links, clock, LOST_AFTER, metrics) { WaitingSteps(0, 0) }
        val log = dispatchLog { MutFlow.underTest { dispatcher.onQueued(TENANT, AGENT.agentId) } }
        assertEquals(emptyList(), links.sentIds())
        val cause = "java.lang.IllegalStateException: database down"
        val expected = "queued steps of agent ${AGENT.agentId} wait for the next check: $cause"
        assertEquals(listOf(expected), log)
    }
}
