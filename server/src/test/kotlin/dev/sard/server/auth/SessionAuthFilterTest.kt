// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.auth

import dev.sard.server.api.SESSION_COOKIE
import dev.sard.server.api.SESSION_REQUEST_ATTRIBUTE
import dev.sard.server.pki.MovableClock
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import org.springframework.mock.web.MockHttpServletRequest
import org.springframework.mock.web.MockHttpServletResponse
import tools.jackson.databind.ObjectMapper
import java.time.Instant
import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNull
import kotlin.test.assertTrue

private val NOW: Instant = Instant.parse("2026-10-01T12:00:00Z")
private val TENANT: UUID = UUID.fromString("00000000-0000-0000-0000-000000000001")

@MutFlowTest
class SessionAuthFilterTest {
    private val clock = MovableClock(NOW)
    private val store = SessionStore(clock)
    private val filter = SessionAuthFilter(store, ObjectMapper())

    private fun filtered(request: MockHttpServletRequest): Pair<MockHttpServletResponse, MockHttpServletRequest?> {
        val response = MockHttpServletResponse()
        var reached: MockHttpServletRequest? = null
        MutFlow.underTest { filter.doFilter(request, response) { req, _ -> reached = req as MockHttpServletRequest } }
        return response to reached
    }

    @Test
    fun `sign-in without a session passes through`() {
        val request = MockHttpServletRequest("POST", "/api/v1/session")
        val (_, reached) = filtered(request)
        assertTrue(reached != null)
    }

    @Test
    fun `status without a session passes through`() {
        val request = MockHttpServletRequest("GET", "/api/v1/status")
        val (_, reached) = filtered(request)
        assertTrue(reached != null)
    }

    @Test
    fun `a protected path without a cookie is 401 unauthenticated`() {
        val request = MockHttpServletRequest("GET", "/api/v1/agents")
        val (response, reached) = filtered(request)
        assertEquals(401, response.status)
        assertTrue(response.contentAsString.contains("unauthenticated"))
        assertNull(reached)
    }

    @Test
    fun `an unknown session id is 401`() {
        val request = MockHttpServletRequest("GET", "/api/v1/agents")
        request.setCookies(jakarta.servlet.http.Cookie(SESSION_COOKIE, "forged-session-id"))
        val (response, reached) = filtered(request)
        assertEquals(401, response.status)
        assertNull(reached)
    }

    @Test
    fun `an unmapped path under api v1 without a session is 401`() {
        val request = MockHttpServletRequest("GET", "/api/v1/no-such-resource")
        val (response, _) = filtered(request)
        assertEquals(401, response.status)
    }

    @Test
    fun `a valid session passes through and the touched session is attached to the request`() {
        val session = store.create(TENANT)
        val request = MockHttpServletRequest("GET", "/api/v1/agents")
        request.setCookies(jakarta.servlet.http.Cookie(SESSION_COOKIE, session.id))
        val (_, reached) = filtered(request)
        assertEquals(session.id, (reached?.getAttribute(SESSION_REQUEST_ATTRIBUTE) as AdminSession?)?.id)
    }

    @Test
    fun `DELETE session with an invalid cookie is 401 and clears the cookie`() {
        val request = MockHttpServletRequest("DELETE", "/api/v1/session")
        request.setCookies(jakarta.servlet.http.Cookie(SESSION_COOKIE, "forged-session-id"))
        val (response, reached) = filtered(request)
        assertEquals(401, response.status)
        assertNull(reached)
        val cookie = response.getCookie(SESSION_COOKIE)
        assertTrue(cookie != null)
        assertEquals("", cookie!!.value)
        assertEquals(0, cookie.maxAge)
        assertFalse(cookie.secure)
    }

    @Test
    fun `DELETE session over HTTPS with an invalid cookie clears a Secure cookie`() {
        val request = MockHttpServletRequest("DELETE", "/api/v1/session")
        request.isSecure = true
        request.setCookies(jakarta.servlet.http.Cookie(SESSION_COOKIE, "forged-session-id"))
        val (response, _) = filtered(request)
        val cookie = response.getCookie(SESSION_COOKIE)
        assertTrue(cookie != null)
        assertTrue(cookie!!.secure)
    }

    @Test
    fun `GET agents with an invalid cookie does not clear the cookie`() {
        val request = MockHttpServletRequest("GET", "/api/v1/agents")
        request.setCookies(jakarta.servlet.http.Cookie(SESSION_COOKIE, "forged-session-id"))
        val (response, _) = filtered(request)
        assertFalse(response.containsHeader("Set-Cookie"))
    }

    @Test
    fun `DELETE of a different path with an invalid cookie does not clear the cookie`() {
        val request = MockHttpServletRequest("DELETE", "/api/v1/agents")
        request.setCookies(jakarta.servlet.http.Cookie(SESSION_COOKIE, "forged-session-id"))
        val (response, _) = filtered(request)
        assertFalse(response.containsHeader("Set-Cookie"))
    }

    @Test
    fun `GET session with an invalid cookie does not clear the cookie`() {
        val request = MockHttpServletRequest("GET", "/api/v1/session")
        request.setCookies(jakarta.servlet.http.Cookie(SESSION_COOKIE, "forged-session-id"))
        val (response, _) = filtered(request)
        assertFalse(response.containsHeader("Set-Cookie"))
    }

    @Test
    fun `the state of the first start and the entering of the code pass through without a session`() {
        for ((method, path) in listOf("GET" to "/api/v1/onboarding", "POST" to "/api/v1/onboarding/setup-session")) {
            val (_, reached) = filtered(MockHttpServletRequest(method, path))
            assertTrue(reached != null, "$method $path")
        }
    }

    @Test
    fun `the steps of the wizard pass through for the controller to check the setup session`() {
        for (path in listOf("/api/v1/onboarding/ca", "/api/v1/onboarding/admin")) {
            val (response, reached) = filtered(MockHttpServletRequest("POST", path))
            assertTrue(reached != null, path)
            assertFalse(response.containsHeader("Set-Cookie"), path)
        }
    }

    @Test
    fun `a wizard path of another method is guarded as any other`() {
        for ((method, path) in listOf("GET" to "/api/v1/onboarding/ca", "PUT" to "/api/v1/onboarding/admin")) {
            val (response, reached) = filtered(MockHttpServletRequest(method, path))
            assertEquals(401, response.status, "$method $path")
            assertNull(reached, "$method $path")
        }
    }

    @Test
    fun `a valid administrator session on the state of the first start is attached to the request`() {
        val session = store.create(TENANT)
        val request = MockHttpServletRequest("GET", "/api/v1/onboarding")
        request.setCookies(jakarta.servlet.http.Cookie(SESSION_COOKIE, session.id))
        val (_, reached) = filtered(request)
        assertEquals(session.id, (reached?.getAttribute(SESSION_REQUEST_ATTRIBUTE) as AdminSession?)?.id)
    }

    @Test
    fun `an invalid administrator session on the wizard is not attached and does not stop the request`() {
        val request = MockHttpServletRequest("POST", "/api/v1/onboarding/ca")
        request.setCookies(jakarta.servlet.http.Cookie(SESSION_COOKIE, "forged-session-id"))
        val (_, reached) = filtered(request)
        assertTrue(reached != null)
        assertNull(reached.getAttribute(SESSION_REQUEST_ATTRIBUTE))
    }

    @Test
    fun `sign-in does not attach the session it carries`() {
        val session = store.create(TENANT)
        val request = MockHttpServletRequest("POST", "/api/v1/session")
        request.setCookies(jakarta.servlet.http.Cookie(SESSION_COOKIE, session.id))
        val (_, reached) = filtered(request)
        assertNull(reached?.getAttribute(SESSION_REQUEST_ATTRIBUTE))
    }
}
