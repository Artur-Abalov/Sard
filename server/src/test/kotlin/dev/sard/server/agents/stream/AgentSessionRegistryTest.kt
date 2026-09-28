// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents.stream

import dev.sard.server.agents.stream.StreamFixtures.HEARTBEAT
import dev.sard.server.agents.stream.StreamFixtures.NOW
import dev.sard.server.agents.stream.StreamFixtures.SETTINGS
import dev.sard.server.agents.stream.StreamFixtures.agent
import dev.sard.server.agents.stream.StreamFixtures.stream
import dev.sard.server.pki.MovableClock
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import kotlinx.coroutines.Job
import java.time.Duration
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNotSame
import kotlin.test.assertNull
import kotlin.test.assertSame
import kotlin.test.assertTrue

private val TICK: Duration = Duration.ofMillis(1)

@MutFlowTest
class AgentSessionRegistryTest {
    private val clock = MovableClock(NOW)
    private val registry = AgentSessionRegistry(clock, SETTINGS)

    private fun claimed(stream: AgentStream = stream()): AgentStream =
        stream.also {
            registry.opened(it)
            assertEquals(Claim.Accepted(replaced = null), registry.claim(it))
        }

    @Test
    fun `the first Hello of an agent takes its slot`() {
        val stream = stream()
        registry.opened(stream)
        assertEquals(Claim.Accepted(replaced = null), MutFlow.underTest { registry.claim(stream) })
        assertSame(stream, registry.session(stream.agent.agentId))
    }

    @Test
    fun `a second stream while the first got a message within the duplicate window is a duplicate`() {
        val first = claimed()
        clock.now = NOW + SETTINGS.duplicateWindow - TICK
        val second = stream(first.agent, clock.now)
        registry.opened(second)

        assertEquals(Claim.Duplicate, MutFlow.underTest { registry.claim(second) })
        assertSame(first, registry.session(first.agent.agentId), "the live session keeps the slot")
        assertNull(first.closedBy)
        assertTrue(first.received(clock.now), "the live session is suspected until it proves alive")
    }

    @Test
    fun `a second stream once the first is silent for the whole window replaces it`() {
        val first = claimed()
        val firstJob = Job().also { first.bind(it) }
        clock.now = NOW + SETTINGS.duplicateWindow
        val second = stream(first.agent, clock.now)
        registry.opened(second)

        assertEquals(Claim.Accepted(replaced = first), MutFlow.underTest { registry.claim(second) })
        assertSame(second, registry.session(first.agent.agentId))
        assertEquals("SESSION_REPLACED", first.closedBy?.reason)
        assertTrue(firstJob.isCancelled, "the replaced stream's coroutine ends")
        assertFalse(first.received(clock.now), "a replaced session is not a duplicate suspect")
    }

    @Test
    fun `freshness counts from the last message, not from the Hello`() {
        val first = claimed()
        first.received(NOW + HEARTBEAT)
        clock.now = NOW + HEARTBEAT + SETTINGS.duplicateWindow - TICK
        val second = stream(first.agent, clock.now)
        registry.opened(second)
        assertEquals(Claim.Duplicate, MutFlow.underTest { registry.claim(second) })
    }

    @Test
    fun `releasing a replaced stream leaves its successor in the slot`() {
        val first = claimed()
        clock.now = NOW + SETTINGS.duplicateWindow
        val second = stream(first.agent, clock.now)
        registry.opened(second)
        registry.claim(second)

        assertFalse(MutFlow.underTest { registry.release(first) })
        assertSame(second, registry.session(first.agent.agentId))
        assertTrue(registry.release(second))
        assertNull(registry.session(first.agent.agentId))
    }

    @Test
    fun `agents do not share slots`() {
        val first = claimed()
        val other = stream(agent())
        registry.opened(other)
        assertEquals(Claim.Accepted(replaced = null), MutFlow.underTest { registry.claim(other) })
        assertSame(first, registry.session(first.agent.agentId))
    }

    @Test
    fun `an agent is online while its session got a message within offlineAfter`() {
        val stream = claimed()
        clock.now = NOW + SETTINGS.offlineAfter - TICK
        assertTrue(MutFlow.underTest { registry.online(stream.agent.agentId) })
    }

    @Test
    fun `an agent whose session is silent for offlineAfter is not online`() {
        val stream = claimed()
        clock.now = NOW + SETTINGS.offlineAfter
        assertFalse(MutFlow.underTest { registry.online(stream.agent.agentId) })
    }

    @Test
    fun `an agent without a session is not online`() {
        assertFalse(MutFlow.underTest { registry.online(agent().agentId) })
    }

    @Test
    fun `a stream before its Hello is not a session and not online`() {
        val stream = stream()
        registry.opened(stream)
        assertNull(MutFlow.underTest { registry.session(stream.agent.agentId) })
        assertFalse(registry.online(stream.agent.agentId))
    }

    @Test
    fun `sweep closes sessions silent for offlineAfter and keeps the others`() {
        val silent = claimed()
        val talking = claimed()
        val silentJob = Job().also { silent.bind(it) }
        val talkingJob = Job().also { talking.bind(it) }
        clock.now = NOW + SETTINGS.offlineAfter
        talking.received(clock.now - TICK)

        assertEquals(listOf(silent), MutFlow.underTest { registry.sweep() })
        assertEquals("SESSION_EXPIRED", silent.closedBy?.reason)
        assertTrue(silentJob.isCancelled)
        assertNull(talking.closedBy)
        assertFalse(talkingJob.isCancelled)
    }

    @Test
    fun `sweep closes streams that sent no Hello within the hello timeout`() {
        val early = stream(openedAt = NOW)
        val late = stream(openedAt = NOW + TICK)
        registry.opened(early)
        registry.opened(late)
        clock.now = NOW + SETTINGS.helloTimeout

        val earlyJob = Job().also { early.bind(it) }
        assertEquals(listOf(early), MutFlow.underTest { registry.sweep() })
        assertEquals("HELLO_REQUIRED", early.closedBy?.reason)
        assertTrue(earlyJob.isCancelled)
        assertNull(late.closedBy)
    }

    @Test
    fun `a claimed stream is no longer subject to the hello timeout`() {
        val stream = claimed()
        stream.received(NOW + SETTINGS.helloTimeout)
        clock.now = NOW + SETTINGS.helloTimeout
        assertEquals(emptyList(), MutFlow.underTest { registry.sweep() })
    }

    @Test
    fun `a released stream before its Hello is forgotten by the sweep`() {
        val stream = stream()
        registry.opened(stream)
        assertFalse(MutFlow.underTest { registry.release(stream) })
        clock.now = NOW + SETTINGS.helloTimeout
        assertEquals(emptyList(), registry.sweep())
    }

    @Test
    fun `sessions and count cover claimed streams only`() {
        val session = claimed()
        registry.opened(stream())
        assertEquals(listOf(session), MutFlow.underTest { registry.sessions() })
        assertEquals(1, registry.count())
    }

    @Test
    fun `closeAll closes sessions and streams before their Hello`() {
        val session = claimed()
        val early = stream()
        registry.opened(early)
        MutFlow.underTest { registry.closeAll(StreamCloseReason.SERVER_SHUTTING_DOWN.close()) }
        assertEquals("SERVER_SHUTTING_DOWN", session.closedBy?.reason)
        assertEquals("SERVER_SHUTTING_DOWN", early.closedBy?.reason)
    }

    @Test
    fun `after closeAll a new stream is closed as soon as it opens`() {
        registry.closeAll(StreamCloseReason.SERVER_SHUTTING_DOWN.close())
        val late = stream()
        MutFlow.underTest { registry.opened(late) }
        assertEquals("SERVER_SHUTTING_DOWN", late.closedBy?.reason)
    }

    @Test
    fun `streams closed together do not share trailers`() {
        val a = claimed()
        val b = claimed()
        registry.closeAll(StreamCloseReason.SERVER_SHUTTING_DOWN.close())
        assertNotSame(a.closedBy?.status()?.trailers, b.closedBy?.status()?.trailers)
    }

    @Test
    fun `after reopen new streams are accepted again`() {
        registry.closeAll(StreamCloseReason.SERVER_SHUTTING_DOWN.close())
        MutFlow.underTest { registry.reopen() }
        val stream = stream()
        registry.opened(stream)
        assertNull(stream.closedBy)
        assertEquals(Claim.Accepted(replaced = null), registry.claim(stream))
    }
}
