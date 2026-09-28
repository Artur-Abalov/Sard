// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import org.springframework.mock.web.MockHttpServletRequest
import org.springframework.mock.web.MockHttpServletResponse
import tools.jackson.databind.ObjectMapper
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

private class ThrowingSessionApi : SessionApi {
    override fun createSession(
        password: String,
        clientAddress: String,
        previousSessionId: String?,
    ) = throw NotImplementedError()

    override fun getSession(sessionId: String): Session = throw NoSuchSessionException()

    override fun deleteSession(
        sessionId: String,
        clientAddress: String,
    ) = throw NoSuchSessionException()
}

/** Р12: only DELETE with an invalid or missing session clears the cookie; GET leaves it. */
class SessionControllerTest {
    private val controller = SessionController(ThrowingSessionApi(), ObjectMapper())

    @Test
    fun `GET with an invalid session is 401 and does not clear the cookie`() {
        val request = MockHttpServletRequest("GET", "/api/v1/session")
        val response = MockHttpServletResponse()
        controller.onNoSuchSession(request, response)
        assertFalse(response.containsHeader("Set-Cookie"))
    }

    @Test
    fun `DELETE with an invalid session is 401 and clears the cookie`() {
        val request = MockHttpServletRequest("DELETE", "/api/v1/session")
        val response = MockHttpServletResponse()
        controller.onNoSuchSession(request, response)
        val cookie = response.getCookie(SESSION_COOKIE)
        assertTrue(cookie != null)
        assertEquals("", cookie!!.value)
    }
}
