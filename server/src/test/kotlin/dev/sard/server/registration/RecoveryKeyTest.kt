// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.registration

import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertTrue

/** V3: the recovery key flag read back from the database. */
@MutFlowTest
class RecoveryKeyTest {
    @Test
    fun `A stored present flag is read as PRESENT`() {
        assertEquals(RecoveryKey.PRESENT, MutFlow.underTest { RecoveryKey.of("present") })
    }

    @Test
    fun `A stored absent flag is read as ABSENT`() {
        assertEquals(RecoveryKey.ABSENT, MutFlow.underTest { RecoveryKey.of("absent") })
    }

    @Test
    fun `A stored unknown flag is read as UNKNOWN`() {
        assertEquals(RecoveryKey.UNKNOWN, MutFlow.underTest { RecoveryKey.of("unknown") })
    }

    @Test
    fun `A flag the database CHECK does not allow is refused and named`() {
        val failure = assertFailsWith<IllegalStateException> { MutFlow.underTest { RecoveryKey.of("maybe") } }

        assertTrue("maybe" in failure.message.orEmpty())
    }
}
