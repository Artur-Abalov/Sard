// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import org.springframework.mock.web.MockHttpServletResponse
import tools.jackson.databind.ObjectMapper
import kotlin.test.Test
import kotlin.test.assertEquals

/**
 * writeProblem (W1b, К1): the single writer used by filters that must answer before Spring
 * MVC picks a handler (session auth, the Origin guard) and by sign-in's non-inferable status.
 */
@MutFlowTest
class WriteProblemTest {
    private val objectMapper = ObjectMapper()

    @Test
    fun `writes the status, RFC 9457 content type and problem body`() {
        val response = MockHttpServletResponse()
        MutFlow.underTest {
            writeProblem(response, objectMapper, 429, "Too Many Requests", ErrorCode.TOO_MANY_ATTEMPTS)
        }
        assertEquals(429, response.status)
        assertEquals(PROBLEM_JSON, response.contentType)
        val body = objectMapper.readTree(response.contentAsByteArray)
        assertEquals("about:blank", body.path("type").asString())
        assertEquals("Too Many Requests", body.path("title").asString())
        assertEquals(429, body.path("status").asInt())
        assertEquals("too_many_attempts", body.path("code").asString())
        assertEquals(true, body.path("detail").isNull)
    }

    @Test
    fun `a different status and code produce a different body`() {
        val response = MockHttpServletResponse()
        MutFlow.underTest { writeProblem(response, objectMapper, 401, "Unauthorized", ErrorCode.UNAUTHENTICATED) }
        assertEquals(401, response.status)
        val body = objectMapper.readTree(response.contentAsByteArray)
        assertEquals("Unauthorized", body.path("title").asString())
        assertEquals(401, body.path("status").asInt())
        assertEquals("unauthenticated", body.path("code").asString())
    }
}
