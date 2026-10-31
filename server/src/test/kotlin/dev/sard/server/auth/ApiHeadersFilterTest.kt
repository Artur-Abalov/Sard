// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.auth

import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import org.springframework.mock.web.MockHttpServletRequest
import org.springframework.mock.web.MockHttpServletResponse
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNull
import kotlin.test.assertTrue

/** Rule "Ответы api v1 несут nosniff и no-store, прочие ответы — прежние заголовки" (Р47). */
@MutFlowTest
class ApiHeadersFilterTest {
    private fun filtered(path: String): Pair<MockHttpServletResponse, Boolean> {
        val response = MockHttpServletResponse()
        var reached = false
        val request = MockHttpServletRequest("GET", path).apply { requestURI = path }
        MutFlow.underTest { ApiHeadersFilter().doFilter(request, response) { _, _ -> reached = true } }
        return response to reached
    }

    @Test
    fun `a response under api v1 gets one nosniff and one no-store`() {
        for (path in listOf("/api/v1/status", "/api/v1/sources/x", "/api/v1")) {
            val (response, reached) = filtered(path)

            assertTrue(reached, path)
            assertEquals(listOf("nosniff"), response.getHeaders("X-Content-Type-Options"), path)
            assertEquals(listOf("no-store"), response.getHeaders("Cache-Control"), path)
            assertEquals(setOf("X-Content-Type-Options", "Cache-Control"), response.headerNames.toSet(), path)
        }
    }

    @Test
    fun `a response outside api v1 gets no header of the API`() {
        val outside = listOf("/", "/agents", "/actuator/health", "/v3/api-docs", "/api", "/api/v2/agents", "/api/v10/x")
        for (path in outside) {
            val (response, reached) = filtered(path)

            assertTrue(reached, path)
            assertNull(response.getHeader("X-Content-Type-Options"), path)
            assertNull(response.getHeader("Cache-Control"), path)
        }
    }

    @Test
    fun `the headers are one each when the response already carried them`() {
        val response = MockHttpServletResponse().apply { addHeader("Cache-Control", "max-age=60") }
        val request = MockHttpServletRequest("GET", "/api/v1/status").apply { requestURI = "/api/v1/status" }

        MutFlow.underTest { ApiHeadersFilter().doFilter(request, response) { _, _ -> } }

        assertEquals(listOf("no-store"), response.getHeaders("Cache-Control"))
    }
}
