// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.auth

import dev.sard.server.api.SESSION_COOKIE
import dev.sard.server.pki.MovableClock
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

class SessionAuthFilterTest {
    private val clock = MovableClock(NOW)
    private val store = SessionStore(clock)
    private val filter = SessionAuthFilter(store, ObjectMapper())

    private fun filtered(request: MockHttpServletRequest): Pair<MockHttpServletResponse, MockHttpServletRequest?> {
        val response = MockHttpServletResponse()
        var reached: MockHttpServletRequest? = null
        filter.doFilter(request, response) { req, _ -> reached = req as MockHttpServletRequest }
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
    }

    @Test
    fun `GET agents with an invalid cookie does not clear the cookie`() {
        val request = MockHttpServletRequest("GET", "/api/v1/agents")
        request.setCookies(jakarta.servlet.http.Cookie(SESSION_COOKIE, "forged-session-id"))
        val (response, _) = filtered(request)
        assertFalse(response.containsHeader("Set-Cookie"))
    }
}
