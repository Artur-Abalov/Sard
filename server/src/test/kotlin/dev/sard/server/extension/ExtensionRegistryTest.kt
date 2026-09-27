// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.extension

import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith

@MutFlowTest
class ExtensionRegistryTest {
    private fun ext(id: String) =
        object : SardExtension {
            override val id = id
            override val displayName = id.uppercase()
        }

    @Test
    fun `an empty registry is valid`() {
        assertEquals(emptyList(), MutFlow.underTest { ExtensionRegistry(emptyList()).ids() })
    }

    @Test
    fun `extensions are ordered by id`() {
        val ids = MutFlow.underTest { ExtensionRegistry(listOf(ext("sso"), ext("audit"))).ids() }
        assertEquals(listOf("audit", "sso"), ids)
    }

    @Test
    fun `duplicate ids are rejected`() {
        val e =
            assertFailsWith<IllegalArgumentException> {
                MutFlow.underTest { ExtensionRegistry(listOf(ext("sso"), ext("sso"))) }
            }
        assertEquals("Duplicate Sard extension ids: [sso]", e.message)
    }
}
