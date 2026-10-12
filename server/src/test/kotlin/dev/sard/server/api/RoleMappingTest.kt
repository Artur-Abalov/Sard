// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import dev.sard.server.pki.AgentRole as DomainRole

@MutFlowTest
class RoleMappingTest {
    @Test
    fun `each wire role maps to the domain role of the same name`() {
        assertEquals(DomainRole.SOURCE, MutFlow.underTest { AgentRole.SOURCE.toDomain() })
        assertEquals(DomainRole.VERIFIER, MutFlow.underTest { AgentRole.VERIFIER.toDomain() })
    }

    @Test
    fun `each domain role maps to the wire role of the same name`() {
        assertEquals(AgentRole.SOURCE, MutFlow.underTest { DomainRole.SOURCE.toApi() })
        assertEquals(AgentRole.VERIFIER, MutFlow.underTest { DomainRole.VERIFIER.toApi() })
    }

    @Test
    fun `only the exact wire spelling is a role`() {
        assertEquals(AgentRole.SOURCE, MutFlow.underTest { AgentRole.fromWire("source") })
        assertEquals(AgentRole.VERIFIER, MutFlow.underTest { AgentRole.fromWire("verifier") })
        for (text in listOf("", "SOURCE", "Verifier", "admin", "1", " source")) {
            assertFailsWith<IllegalArgumentException>(text) { AgentRole.fromWire(text) }
        }
    }
}
