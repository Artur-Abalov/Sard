// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents.stream

import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import java.time.Duration
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith

private val BEAT: Duration = Duration.ofSeconds(30)

@MutFlowTest
class AgentStreamPropertiesTest {
    @Test
    fun `the defaults count windows in heartbeat intervals`() {
        val settings = MutFlow.underTest { AgentStreamProperties().settings(BEAT) }
        val expected =
            AgentStreamSettings(
                heartbeatInterval = BEAT,
                duplicateWindow = Duration.ofSeconds(60),
                offlineAfter = Duration.ofSeconds(90),
                helloTimeout = BEAT,
                sendQueue = 64,
                checkInterval = BEAT,
                clockSkewThreshold = BEAT,
            )
        assertEquals(expected, settings)
    }

    @Test
    fun `the smallest valid windows are accepted`() {
        val properties = AgentStreamProperties(duplicateWindowHeartbeats = 1, offlineAfterHeartbeats = 2, sendQueue = 1)
        val settings = MutFlow.underTest { properties.settings(Duration.ofNanos(1)) }
        assertEquals(Duration.ofNanos(1), settings.duplicateWindow)
        assertEquals(Duration.ofNanos(2), settings.offlineAfter)
    }

    @Test
    fun `a heartbeat interval that is not positive is refused`() {
        val properties = AgentStreamProperties()
        for (interval in listOf(Duration.ZERO, Duration.ofSeconds(-1))) {
            assertFailsWith<IllegalArgumentException> { MutFlow.underTest { properties.settings(interval) } }
        }
    }

    @Test
    fun `a duplicate window below one interval is refused`() {
        val properties = AgentStreamProperties(duplicateWindowHeartbeats = 0)
        assertFailsWith<IllegalArgumentException> { MutFlow.underTest { properties.settings(BEAT) } }
    }

    @Test
    fun `a duplicate window as long as the offline window is refused`() {
        val properties = AgentStreamProperties(duplicateWindowHeartbeats = 3, offlineAfterHeartbeats = 3)
        assertFailsWith<IllegalArgumentException> { MutFlow.underTest { properties.settings(BEAT) } }
    }

    @Test
    fun `an empty send queue is refused`() {
        val properties = AgentStreamProperties(sendQueue = 0)
        assertFailsWith<IllegalArgumentException> { MutFlow.underTest { properties.settings(BEAT) } }
    }
}
