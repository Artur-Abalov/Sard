// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents.dispatch

import dev.sard.server.agents.stream.STREAM_ENDED
import dev.sard.server.pki.MovableClock
import dev.sard.server.runs.StepState
import dev.sard.server.runs.WaitingSteps
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
        val dispatcher = StepDispatcher(ledger, ledger, FakeLinks(), clock, LOST_AFTER, metrics) { WaitingSteps(0, 0) }

        MutFlow.underTest { StepLossOnDisconnect(dispatcher).disconnected(AGENT, STREAM_ENDED) }

        assertEquals(DISPATCH_NOW + LOST_AFTER, ledger.deadline(1))
    }
}
