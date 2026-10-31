// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.auth

import dev.sard.server.extension.Subject
import dev.sard.server.extension.SubjectAuthentication
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import org.springframework.security.authentication.TestingAuthenticationToken
import org.springframework.security.core.Authentication
import org.springframework.security.core.context.SecurityContextHolder
import kotlin.test.AfterTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNull

private val SSO_USER = Subject("sso-user", "alice")

/** Rule "Кто выполняет запрос, политика и события узнают из одного шва SubjectSource" (Р26, Р39): the core's source. */
@MutFlowTest
class CoreSubjectSourceTest {
    @AfterTest
    fun `leave no authentication on the thread`() = SecurityContextHolder.clearContext()

    private fun subjectOf(authentication: Authentication?): Subject? {
        SecurityContextHolder.getContext().authentication = authentication
        return MutFlow.underTest { CoreSubjectSource().current() }
    }

    @Test
    fun `the subject of a subject authentication is the subject of the request`() {
        assertEquals(SSO_USER, subjectOf(SubjectAuthentication(SSO_USER)))
    }

    @Test
    fun `the subject of the core session is the administrator`() {
        assertEquals(Subject.ADMINISTRATOR, subjectOf(SubjectAuthentication(Subject.ADMINISTRATOR)))
    }

    @Test
    fun `an authentication that is not authenticated names no subject`() {
        assertNull(subjectOf(SubjectAuthentication(SSO_USER, authenticated = false)))
    }

    @Test
    fun `an authentication of another type names no subject`() {
        assertNull(subjectOf(TestingAuthenticationToken("alice", "x").apply { isAuthenticated = true }))
    }

    @Test
    fun `without an authentication there is no subject, as outside a request`() {
        assertNull(subjectOf(null))
    }
}
