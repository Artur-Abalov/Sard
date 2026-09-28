// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith

@MutFlowTest
class WireEnumConverterFactoryTest {
    private enum class Plain { ONE }

    @Test
    fun `a query value is the enum's wire value`() {
        val converter = WireEnumConverterFactory().getConverter(StepStatus::class.java)
        assertEquals(StepStatus.TIMED_OUT, MutFlow.underTest { converter.convert("timed_out") })
    }

    @Test
    fun `the constant name is not a wire value`() {
        val converter = WireEnumConverterFactory().getConverter(StepStatus::class.java)
        val e = assertFailsWith<IllegalArgumentException> { MutFlow.underTest { converter.convert("TIMED_OUT") } }
        assertEquals("unknown StepStatus: TIMED_OUT", e.message)
    }

    @Test
    fun `an enum without wire names takes its constant names`() {
        val converter = WireEnumConverterFactory().getConverter(Plain::class.java)
        assertEquals(Plain.ONE, MutFlow.underTest { converter.convert("ONE") })
    }
}
