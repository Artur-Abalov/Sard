// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.persistence

import dev.sard.server.extension.TenantResolver
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

@MutFlowTest
class HibernateTenantBridgeTest {
    private val tenant = UUID.fromString("7f3c1a52-0b4e-4c1d-9a55-2d8e6f0b9c11")
    private val bridge = HibernateTenantBridge { tenant }

    @Test
    fun `hibernate sees the tenant the resolver names`() {
        assertEquals(tenant, MutFlow.underTest { bridge.resolveCurrentTenantIdentifier() })
    }

    @Test
    fun `a session opened for one tenant is not reused for another`() {
        assertTrue(MutFlow.underTest { bridge.validateExistingCurrentSessions() })
    }

    @Test
    fun `no tenant is root`() {
        // Root would switch the tenant filter off; cross-tenant access is not designed yet (ADR 0013).
        assertFalse(bridge.isRoot(tenant))
    }
}
