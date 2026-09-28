// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents.stream

import dev.sard.server.agents.stream.StreamFixtures.NOW
import dev.sard.server.agents.stream.StreamFixtures.SETTINGS
import dev.sard.server.agents.stream.StreamFixtures.stream
import dev.sard.server.pki.MovableClock
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import io.micrometer.core.instrument.simple.SimpleMeterRegistry
import java.time.Duration
import kotlin.test.Test
import kotlin.test.assertEquals

@MutFlowTest
class MicrometerStreamMetricsTest {
    private val meters = SimpleMeterRegistry()
    private val registry = AgentSessionRegistry(MovableClock(NOW), SETTINGS)

    @Test
    fun `sard_agents_connected follows the sessions in the registry`() {
        MicrometerStreamMetrics(meters, registry)
        val gauge = meters.get("sard.agents.connected").gauge()
        assertEquals(0.0, gauge.value())
        val session = stream().also { registry.claim(it) }
        registry.claim(stream())
        assertEquals(2.0, gauge.value())
        registry.release(session)
        assertEquals(1.0, gauge.value())
    }

    @Test
    fun `duplicates are counted`() {
        val metrics = MicrometerStreamMetrics(meters, registry)
        MutFlow.underTest { metrics.duplicateDetected() }
        assertEquals(1.0, meters.get("sard.agent.duplicate.sessions").counter().count())
    }

    @Test
    fun `clock skew is recorded in seconds, either way`() {
        val metrics = MicrometerStreamMetrics(meters, registry)
        MutFlow.underTest {
            metrics.clockSkew(Duration.ofMillis(-1500))
            metrics.clockSkew(Duration.ofMillis(500))
        }
        val summary = meters.get("sard.agent.clock.skew").summary()
        assertEquals(2, summary.count())
        assertEquals(2.0, summary.totalAmount())
        assertEquals(1.5, summary.max())
    }
}
