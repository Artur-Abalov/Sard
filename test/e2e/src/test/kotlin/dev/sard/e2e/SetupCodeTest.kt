// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.e2e

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNull

/** Finding the setup code in a server log (docs/specs/server/onboarding-setup.feature, Р1). */
class SetupCodeTest {
    private val first = "ABCD-EFGH-JKMN-PQRS-TVWX-YZ01-2345"
    private val second = "0123-4567-89AB-CDEF-GHJK-MNPQ-RSTV"

    @Test
    fun `the code is read from the line the server prints`() {
        val log = "INFO ... : SARD SETUP CODE: $first valid until 2026-10-10T12:00:00Z\nINFO other"
        assertEquals(first, SetupCode.lastIn(log))
    }

    @Test
    fun `the code of the last start wins`() {
        val log = "SARD SETUP CODE: $first valid until a\nrestart\nSARD SETUP CODE: $second valid until b"
        assertEquals(second, SetupCode.lastIn(log))
    }

    @Test
    fun `no line or a code outside the alphabet is no code`() {
        assertNull(SetupCode.lastIn("nothing here"))
        assertNull(SetupCode.lastIn("SARD SETUP CODE: ILOU-ILOU-ILOU-ILOU-ILOU-ILOU-ILOU valid until x"))
    }

    @Test
    fun `the number of code lines is counted`() {
        val log = "SARD SETUP CODE: $first valid until a\nSARD SETUP CODE: $second valid until b"
        assertEquals(2, SetupCode.count(log))
        assertEquals(0, SetupCode.count("none"))
    }
}
