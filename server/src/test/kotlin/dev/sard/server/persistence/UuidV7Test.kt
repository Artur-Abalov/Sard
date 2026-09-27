// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.persistence

import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import java.time.Clock
import java.time.Instant
import java.time.ZoneOffset
import java.util.Random
import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNotEquals

/** A source of random bits that always yields the same bit. */
private class ConstantBits(
    private val one: Boolean,
) : Random() {
    override fun nextLong(): Long = if (one) -1L else 0L
}

@MutFlowTest
class UuidV7Test {
    // 0x0192_3ff0_2d40 milliseconds since the epoch.
    private val clock = Clock.fixed(Instant.ofEpochMilli(0x01923ff02d40L), ZoneOffset.UTC)

    @Test
    fun `the timestamp, version and variant sit where RFC 9562 puts them`() {
        val zeros = MutFlow.underTest { UuidV7(clock, ConstantBits(one = false)).next() }
        assertEquals(UUID.fromString("01923ff0-2d40-7000-8000-000000000000"), zeros)
    }

    @Test
    fun `random bits fill everything else`() {
        val ones = MutFlow.underTest { UuidV7(clock, ConstantBits(one = true)).next() }
        assertEquals(UUID.fromString("01923ff0-2d40-7fff-bfff-ffffffffffff"), ones)
        assertEquals(7, ones.version())
        assertEquals(2, ones.variant())
    }

    @Test
    fun `two ids in the same millisecond differ`() {
        val ids = UuidV7(clock, Random(1))
        assertNotEquals(MutFlow.underTest { ids.next() }, ids.next())
    }
}
