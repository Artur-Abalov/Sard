// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents.dispatch

import dev.sard.server.agents.stream.STREAM_ENDED
import dev.sard.server.pki.MovableClock
import dev.sard.server.runs.StepState
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import org.springframework.context.SmartLifecycle
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

@MutFlowTest
class StepLossHooksTest {
    @Test
    fun `the start hook runs once on start, before the gRPC server's phase`() {
        var starts = 0
        val hook = StepLossOnStart { starts++ }
        assertFalse(hook.isRunning)

        MutFlow.underTest { hook.start() }
        assertEquals(1, starts)
        assertTrue(hook.isRunning)

        MutFlow.underTest { hook.stop() }
        assertFalse(hook.isRunning)
        // GrpcServerLifecycle.getPhase() is Integer.MAX_VALUE, the default phase (spring-grpc-core 1.1.1).
        assertTrue(hook.phase < SmartLifecycle.DEFAULT_PHASE)
    }

    @Test
    fun `the session listener hands the end of a session to the dispatcher`() {
        val clock = MovableClock(DISPATCH_NOW)
        val ledger = FakeLedger(step(1, StepState.RUNNING), now = { clock.now })
        val metrics = RecordingDispatchMetrics()
        val dispatcher =
            dispatcherOf(ledger, ledger, FakeLinks(), clock, metrics, ledger)

        MutFlow.underTest { StepLossOnDisconnect(dispatcher).disconnected(AGENT, STREAM_ENDED) }

        assertEquals(DISPATCH_NOW + LOST_AFTER, ledger.deadline(1))
    }

    @Test
    fun `a session that ended is sent the CancelStep again when the next one begins`() {
        val clock = MovableClock(DISPATCH_NOW)
        val ledger = FakeLedger(step(1, StepState.RUNNING, cancelRequested = true), now = { clock.now })
        val links = FakeLinks().also { it.online += AGENT.agentId }
        val dispatcher =
            dispatcherOf(ledger, ledger, links, clock, RecordingDispatchMetrics(), ledger)
        dispatcher.onCancelled(TENANT, AGENT.agentId)

        MutFlow.underTest { StepLossOnDisconnect(dispatcher).disconnected(AGENT, STREAM_ENDED) }
        dispatcher.onCancelled(TENANT, AGENT.agentId)

        assertEquals(listOf(1L, 1L), links.cancelledIds())
    }
}
