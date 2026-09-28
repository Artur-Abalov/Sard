// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.auth

import org.springframework.mock.web.MockHttpServletRequest
import org.springframework.mock.web.MockHttpServletResponse
import tools.jackson.databind.ObjectMapper
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

private const val API_SOURCES = "/api/v1/sources"
private const val API_SESSION = "/api/v1/session"

class OriginGuardFilterTest {
    private val filter = OriginGuardFilter(ObjectMapper())

    private fun request(
        method: String,
        uri: String,
        host: String = "sard.example.com:8080",
        secure: Boolean = false,
    ) = MockHttpServletRequest(method, uri).apply {
        addHeader("Host", host)
        isSecure = secure
        serverName = host.substringBefore(':')
    }

    private fun filtered(request: MockHttpServletRequest): Pair<MockHttpServletResponse, Boolean> {
        val response = MockHttpServletResponse()
        var reachedChain = false
        filter.doFilter(request, response) { _, _ -> reachedChain = true }
        return response to reachedChain
    }

    @Test
    fun `a mutating request with a foreign Origin is rejected with 403 origin_rejected`() {
        val request = request("DELETE", API_SESSION)
        request.addHeader("Origin", "https://evil.example")
        val (response, reached) = filtered(request)
        assertEquals(403, response.status)
        assertEquals("application/problem+json", response.contentType)
        assertTrue(response.contentAsString.contains("origin_rejected"))
        assertFalse(reached)
    }

    @Test
    fun `Origin null is rejected`() {
        val request = request("DELETE", API_SESSION)
        request.addHeader("Origin", "null")
        val (response, _) = filtered(request)
        assertEquals(403, response.status)
    }

    @Test
    fun `a matching Origin (scheme, Host header, port) is allowed`() {
        val request = request("DELETE", API_SESSION, host = "sard.example.com:8080")
        request.addHeader("Origin", "http://sard.example.com:8080")
        val (_, reached) = filtered(request)
        assertTrue(reached)
    }

    @Test
    fun `the dev proxy origin against its own Host is allowed`() {
        val request = request("DELETE", API_SESSION, host = "localhost:5173")
        request.addHeader("Origin", "http://localhost:5173")
        val (_, reached) = filtered(request)
        assertTrue(reached)
    }

    @Test
    fun `a matching host but different port is rejected`() {
        val request = request("DELETE", API_SESSION, host = "sard.example.com:8080")
        request.addHeader("Origin", "http://sard.example.com:9999")
        val (response, reached) = filtered(request)
        assertEquals(403, response.status)
        assertFalse(reached)
    }

    @Test
    fun `no Origin header is allowed`() {
        val request = request("DELETE", API_SESSION)
        val (_, reached) = filtered(request)
        assertTrue(reached)
    }

    @Test
    fun `a GET with a foreign Origin is allowed`() {
        val request = request("GET", API_SOURCES)
        request.addHeader("Origin", "https://evil.example")
        val (_, reached) = filtered(request)
        assertTrue(reached)
    }

    @Test
    fun `X-Forwarded-Proto is not trusted for the scheme half of the comparison`() {
        val request = request("DELETE", API_SESSION, host = "sard.example.com:8080", secure = false)
        request.addHeader("X-Forwarded-Proto", "https")
        request.addHeader("Origin", "https://sard.example.com:8080")
        val (response, reached) = filtered(request)
        assertEquals(403, response.status)
        assertFalse(reached)
    }
}
