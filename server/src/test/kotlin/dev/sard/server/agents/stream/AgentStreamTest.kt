// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents.stream

import dev.sard.proto.agent.v1.ConnectResponse
import dev.sard.server.agents.stream.StreamFixtures.HEARTBEAT
import dev.sard.server.agents.stream.StreamFixtures.NOW
import dev.sard.server.agents.stream.StreamFixtures.stream
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import kotlinx.coroutines.Job
import java.time.Duration
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNull
import kotlin.test.assertTrue

@MutFlowTest
class AgentStreamTest {
    @Test
    fun `a message moves lastMessageAt and, unsuspected, confirms nothing`() {
        val stream = stream()
        assertFalse(MutFlow.underTest { stream.received(NOW.plusSeconds(1)) })
        assertEquals(NOW.plusSeconds(1), stream.lastMessageAt)
    }

    @Test
    fun `a message after a duplicate suspicion confirms it`() {
        val stream = stream()
        stream.suspectDuplicate()
        assertTrue(MutFlow.underTest { stream.received(NOW.plusSeconds(2)) }, "the old session is alive")
    }

    @Test
    fun `one suspicion is confirmed once`() {
        val stream = stream()
        stream.suspectDuplicate()
        stream.received(NOW.plusSeconds(2))
        assertFalse(MutFlow.underTest { stream.received(NOW.plusSeconds(3)) }, "one rejection, one report")
    }

    @Test
    fun `last_seen_at is due on the first message`() {
        assertTrue(MutFlow.underTest { stream().lastSeenDue(NOW, HEARTBEAT) })
    }

    @Test
    fun `a write is remembered, so last_seen_at is not due within the interval`() {
        val stream = stream()
        val secondDue =
            MutFlow.underTest {
                stream.lastSeenDue(NOW, HEARTBEAT)
                stream.lastSeenDue(NOW + HEARTBEAT - Duration.ofMillis(1), HEARTBEAT)
            }
        assertFalse(secondDue)
    }

    @Test
    fun `last_seen_at is due again exactly one interval after the last write`() {
        val stream = stream()
        stream.lastSeenDue(NOW, HEARTBEAT)
        stream.lastSeenDue(NOW + HEARTBEAT - Duration.ofMillis(1), HEARTBEAT)
        assertTrue(MutFlow.underTest { stream.lastSeenDue(NOW + HEARTBEAT, HEARTBEAT) })
    }

    @Test
    fun `the interval counts from the last write, not the last check`() {
        val stream = stream()
        stream.lastSeenDue(NOW, HEARTBEAT)
        stream.lastSeenDue(NOW + HEARTBEAT, HEARTBEAT)
        assertFalse(MutFlow.underTest { stream.lastSeenDue(NOW + HEARTBEAT, HEARTBEAT) })
    }

    @Test
    fun `clock skew is reported once per stream`() {
        val stream = stream()
        assertTrue(MutFlow.underTest { stream.firstSkewReport() })
        assertFalse(stream.firstSkewReport())
    }

    @Test
    fun `the first close wins and cancels the bound job, even when it came before the binding`() {
        val stream = stream()
        MutFlow.underTest { stream.close(StreamCloseReason.SESSION_EXPIRED.close()) }
        stream.close(StreamCloseReason.SESSION_REPLACED.close())
        val job = Job()
        stream.bind(job)
        assertTrue(job.isCancelled)
        assertEquals("SESSION_EXPIRED", stream.closedBy?.reason)
    }

    @Test
    fun `a close after binding cancels the job`() {
        val stream = stream()
        val job = Job()
        stream.bind(job)
        assertFalse(job.isCancelled)
        assertNull(stream.closedBy)
        MutFlow.underTest { stream.close(StreamCloseReason.SESSION_EXPIRED.close()) }
        assertTrue(job.isCancelled)
    }

    @Test
    fun `the outbox takes up to the queue size, then answers QueueFull`() {
        val stream = stream()
        val message = ConnectResponse.getDefaultInstance()
        repeat(StreamFixtures.SETTINGS.sendQueue) {
            assertEquals(SendResult.Queued, stream.offer(message), "message $it")
        }
        assertEquals(SendResult.QueueFull, MutFlow.underTest { stream.offer(message) })
    }

    @Test
    fun `a stream the server closed takes nothing`() {
        val stream = stream()
        stream.close(StreamCloseReason.SESSION_EXPIRED.close())
        assertEquals(SendResult.NotConnected, MutFlow.underTest { stream.offer(ConnectResponse.getDefaultInstance()) })
    }

    @Test
    fun `a stream the agent ended takes nothing`() {
        val stream = stream()
        stream.complete()
        assertEquals(SendResult.NotConnected, MutFlow.underTest { stream.offer(ConnectResponse.getDefaultInstance()) })
    }
}
