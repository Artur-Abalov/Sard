// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents

import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import java.time.Clock
import java.time.Instant
import java.time.ZoneOffset
import kotlin.test.Test
import kotlin.test.assertEquals

/** A8d-3 (docs/specs/server/agent-probe.feature): the server's answer to Probe is its clock. */
@MutFlowTest
class AgentProbeTest {
    private fun answer(clock: Clock) = MutFlow.underTest { AgentProbe(clock).answer() }

    private fun at(text: String) = Clock.fixed(Instant.parse(text), ZoneOffset.UTC)

    @Test
    fun `server time in the answer matches the clock to the nanosecond`() {
        val time = answer(at("2026-10-14T12:00:00.999999999Z")).serverTime
        assertEquals(1791979200L, time.seconds)
        assertEquals(999_999_999, time.nanos)
    }

    @Test
    fun `server time is read from the clock at the moment of the answer, not at start`() {
        val clock = MutableClock(Instant.parse("2026-10-14T11:00:00Z"))
        val probe = AgentProbe(clock)
        clock.now = Instant.parse("2026-10-14T12:00:00.123456789Z")
        val time = MutFlow.underTest { probe.answer() }.serverTime
        assertEquals(1791979200L, time.seconds)
        assertEquals(123_456_789, time.nanos)
    }

    @Test
    fun `a time before the Unix epoch is passed on without distortion`() {
        val time = answer(at("1969-12-31T23:59:59.5Z")).serverTime
        assertEquals(-1L, time.seconds)
        assertEquals(500_000_000, time.nanos)
    }

    @Test
    fun `the answer carries the server time and no other field`() {
        val response = answer(at("2026-10-14T12:00:00Z"))
        assertEquals(
            setOf(1),
            response.allFields.keys
                .map { it.number }
                .toSet(),
        )
    }
}

private class MutableClock(
    var now: Instant,
) : Clock() {
    override fun getZone() = ZoneOffset.UTC

    override fun withZone(zone: java.time.ZoneId) = this

    override fun instant() = now
}
