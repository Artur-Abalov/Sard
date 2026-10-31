// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.extension

import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNull
import kotlin.test.assertTrue

private val ALICE = Subject("sso-user", "alice")

/** Rule "Субъект" (Р39): the authentication that carries the subject of a signed-in user. */
@MutFlowTest
class SubjectAuthenticationTest {
    @Test
    fun `a subject authentication is authenticated and carries the subject`() {
        val authentication = MutFlow.underTest { SubjectAuthentication(ALICE) }

        assertTrue(authentication.isAuthenticated)
        assertEquals(ALICE, authentication.subject)
        assertEquals(ALICE, authentication.principal)
        assertEquals("alice", authentication.name)
        assertNull(authentication.credentials)
        assertTrue(authentication.authorities.isEmpty())
    }

    @Test
    fun `a subject authentication can be made not authenticated`() {
        val authentication = MutFlow.underTest { SubjectAuthentication(ALICE, authenticated = false) }

        assertFalse(authentication.isAuthenticated)
    }
}
