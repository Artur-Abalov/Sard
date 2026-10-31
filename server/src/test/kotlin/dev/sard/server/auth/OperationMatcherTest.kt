// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.auth

import dev.sard.server.extension.Operation
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import org.springframework.mock.web.MockHttpServletRequest
import kotlin.test.Test
import kotlin.test.assertFalse
import kotlin.test.assertTrue

/** Rule "Без сессии открыто ровно то же, что раньше" (Р35): an operation is the method and the exact path. */
@MutFlowTest
class OperationMatcherTest {
    private fun matches(
        matcher: OperationMatcher,
        method: String,
        path: String,
    ): Boolean {
        val request = MockHttpServletRequest(method, path).apply { requestURI = path }
        return MutFlow.underTest { matcher.matches(request) }
    }

    @Test
    fun `an operation matches its method and its exact path`() {
        val matcher = OperationMatcher(setOf("GET /api/v1/status"))

        assertTrue(matches(matcher, "GET", "/api/v1/status"))
    }

    @Test
    fun `another method, a trailing slash, another case or a longer path do not match`() {
        val matcher = OperationMatcher(setOf("GET /api/v1/status"))
        val others =
            listOf(
                "HEAD" to "/api/v1/status",
                "POST" to "/api/v1/status",
                "GET" to "/api/v1/status/",
                "GET" to "/api/v1/STATUS",
                "GET" to "/api/v1/status/extra",
                "GET" to "/api/v1",
            )

        for ((method, path) in others) {
            assertFalse(matches(matcher, method, path), "$method $path")
        }
    }

    @Test
    fun `any of the operations matches`() {
        val matcher = OperationMatcher(setOf("GET /api/v1/status", "POST /api/v1/session"))

        assertTrue(matches(matcher, "POST", "/api/v1/session"))
        assertFalse(matches(matcher, "GET", "/api/v1/session"))
    }

    @Test
    fun `a template with a variable matches a path that fills it`() {
        val matcher = OperationMatcher(listOf(Operation("GET", "/api/v1/things/{id}")))

        assertTrue(matches(matcher, "GET", "/api/v1/things/42"))
        assertFalse(matches(matcher, "GET", "/api/v1/things"))
        assertFalse(matches(matcher, "GET", "/api/v1/things/42/"))
    }

    @Test
    fun `no operations match nothing`() {
        assertFalse(matches(OperationMatcher(emptySet<String>()), "GET", "/api/v1/status"))
    }
}
