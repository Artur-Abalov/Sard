// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.persistence

import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import kotlin.test.Test
import kotlin.test.assertEquals

@MutFlowTest
class HqlTest {
    @Test
    fun `no conditions is no where clause`() {
        assertEquals("", MutFlow.underTest { hqlWhere(emptyList()) })
    }

    @Test
    fun `conditions are all required`() {
        assertEquals("where a = 1", MutFlow.underTest { hqlWhere(listOf("a = 1")) })
        assertEquals("where a = 1 and b = 2", MutFlow.underTest { hqlWhere(listOf("a = 1", "b = 2")) })
    }
}
