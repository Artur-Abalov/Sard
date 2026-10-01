// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.runs

import dev.sard.server.pki.MovableClock
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import java.time.Duration
import java.time.Instant
import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertEquals

private val START: Instant = Instant.parse("2026-09-30T10:00:00Z")
private val INTERVAL: Duration = Duration.ofSeconds(5)
private const val PHASE = "uploading"

/**
 * Progress of one step is written at most once per interval, a new phase at once (S7a, answer 8).
 * Earlier messages are set up outside the code under test, so each mutant meets one decision.
 */
@MutFlowTest
class ProgressThrottleTest {
    private val clock = MovableClock(START)
    private val throttle = ProgressThrottle(clock, INTERVAL, capacity = 3)
    private val agent = UUID.randomUUID()
    private val step = UUID.randomUUID()

    private fun seen(
        stepId: UUID = step,
        phase: String? = PHASE,
    ) = throttle.admit(agent, stepId, phase)

    private fun admit(
        phase: String? = PHASE,
        stepId: UUID = step,
        agentId: UUID = agent,
    ) = MutFlow.underTest { throttle.admit(agentId, stepId, phase) }

    @Test
    fun `the first progress of a step is written`() {
        assertEquals(true, admit())
    }

    @Test
    fun `a repeat within the interval is not written`() {
        seen()
        clock.now = START + INTERVAL.minusMillis(1)

        assertEquals(false, admit())
    }

    @Test
    fun `the next write is due once the interval has passed`() {
        seen()
        clock.now = START + INTERVAL

        assertEquals(true, admit())
    }

    @Test
    fun `a new phase is written at once`() {
        seen(phase = "dumping")

        assertEquals(true, admit())
    }

    @Test
    fun `a written message starts a new interval`() {
        seen(phase = "dumping")
        seen()
        clock.now = START + INTERVAL.minusMillis(1)

        assertEquals(false, admit())
    }

    @Test
    fun `steps and agents are throttled apart`() {
        seen()

        assertEquals(true, admit(stepId = UUID.randomUUID()))
        assertEquals(true, admit(agentId = UUID.randomUUID()))
    }

    @Test
    fun `a forgotten step is written again at once`() {
        seen()
        MutFlow.underTest { throttle.forget(agent, step) }

        assertEquals(true, seen())
    }

    @Test
    fun `when full, entries older than the interval make room`() {
        val old = List(3) { UUID.randomUUID() }
        old.forEach { seen(stepId = it) }
        clock.now = START + INTERVAL

        admit(stepId = UUID.randomUUID())

        assertEquals(1, throttle.size())
        assertEquals(true, seen(stepId = old.first()))
    }

    @Test
    fun `below capacity, old entries stay`() {
        val old = UUID.randomUUID()
        seen(stepId = old)
        clock.now = START + INTERVAL

        admit(stepId = UUID.randomUUID())

        assertEquals(2, throttle.size())
    }

    @Test
    fun `when full of recent entries, a new step is still written`() {
        List(3) { UUID.randomUUID() }.forEach { seen(stepId = it) }

        assertEquals(true, admit(stepId = UUID.randomUUID()))
        assertEquals(4, throttle.size())
    }

    @Test
    fun `a known step makes no room`() {
        List(3) { UUID.randomUUID() }.forEach { seen(stepId = it) }
        seen()
        clock.now = START + INTERVAL

        admit()

        assertEquals(4, throttle.size())
    }
}
