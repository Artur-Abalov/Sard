// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.auth

import dev.sard.server.api.SESSION_REQUEST_ATTRIBUTE
import dev.sard.server.extension.AccessPolicy
import dev.sard.server.extension.Operation
import dev.sard.server.extension.SUBJECT_REQUEST_ATTRIBUTE
import dev.sard.server.extension.Subject
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import org.springframework.beans.factory.ObjectProvider
import org.springframework.beans.factory.support.StaticListableBeanFactory
import org.springframework.mock.web.MockHttpServletRequest
import org.springframework.mock.web.MockHttpServletResponse
import org.springframework.web.servlet.HandlerMapping
import tools.jackson.databind.ObjectMapper
import java.time.Instant
import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

private const val PROTECTED = "/api/v1/sources"

private fun <T : Any> providerOf(
    type: Class<T>,
    bean: T,
): ObjectProvider<T> = StaticListableBeanFactory(mapOf(type.simpleName to bean)).getBeanProvider(type)

@MutFlowTest
class AccessPolicyInterceptorTest {
    private fun interceptor(policy: AccessPolicy): AccessPolicyInterceptor {
        val mapper = providerOf(ObjectMapper::class.java, ObjectMapper())
        return AccessPolicyInterceptor(providerOf(AccessPolicy::class.java, policy), mapper)
    }

    private fun request(
        method: String,
        template: String?,
        session: Boolean,
    ) = MockHttpServletRequest(method, template ?: PROTECTED).apply {
        template?.let { setAttribute(HandlerMapping.BEST_MATCHING_PATTERN_ATTRIBUTE, it) }
        if (session) {
            val now = Instant.parse("2026-09-01T03:00:00Z")
            setAttribute(SESSION_REQUEST_ATTRIBUTE, AdminSession("s", UUID.randomUUID(), now, now))
        }
    }

    private fun handled(
        policy: AccessPolicy,
        request: MockHttpServletRequest,
    ): Pair<Boolean, MockHttpServletResponse> {
        val response = MockHttpServletResponse()
        val allowed = MutFlow.underTest { interceptor(policy).preHandle(request, response, Any()) }
        return allowed to response
    }

    @Test
    fun `a replaced session filter passes its own subject to the policy`() {
        val asked = mutableListOf<Pair<Subject, Operation>>()
        val request =
            request("GET", PROTECTED, session = false).apply {
                setAttribute(SUBJECT_REQUEST_ATTRIBUTE, Subject("sso-user", "alice"))
            }

        val (allowed, response) = handled({ subject, operation -> asked.add(subject to operation) }, request)

        assertTrue(allowed)
        assertEquals(200, response.status)
        assertEquals(listOf(Subject("sso-user", "alice") to Operation("GET", PROTECTED)), asked)
    }

    @Test
    fun `a subject of another type under the attribute is not a subject`() {
        val request = request("GET", PROTECTED, session = false)
        request.setAttribute(SUBJECT_REQUEST_ATTRIBUTE, "alice")

        val (allowed, response) = handled({ _, _ -> true }, request)

        assertFalse(allowed)
        assertEquals(401, response.status)
    }

    @Test
    fun `a protected operation without a core session is refused with 401 even when the policy allows everything`() {
        val (allowed, response) = handled({ _, _ -> true }, request("GET", PROTECTED, session = false))
        assertFalse(allowed)
        assertEquals(401, response.status)
        assertTrue("unauthenticated" in response.contentAsString, response.contentAsString)
    }

    @Test
    fun `a public operation without a session is not asked`() {
        val (allowed, response) = handled({ _, _ -> false }, request("GET", "/api/v1/status", session = false))
        assertTrue(allowed)
        assertEquals(200, response.status)
    }

    @Test
    fun `sign-out and every wizard operation without a session are not asked`() {
        assertTrue(WIZARD_OPERATIONS.isNotEmpty())
        for (operation in WIZARD_OPERATIONS + "DELETE /api/v1/session") {
            val (method, template) = operation.split(" ")
            assertTrue(handled({ _, _ -> false }, request(method, template, session = false)).first, operation)
        }
    }

    @Test
    fun `a request that matched no template is not asked`() {
        assertTrue(handled({ _, _ -> false }, request("GET", null, session = false)).first)
    }

    @Test
    fun `a session and a policy that allows pass`() {
        assertTrue(handled({ _, _ -> true }, request("GET", PROTECTED, session = true)).first)
    }

    @Test
    fun `a session and a policy that refuses get 403 access_denied`() {
        val (allowed, response) = handled({ _, _ -> false }, request("GET", PROTECTED, session = true))
        assertFalse(allowed)
        assertEquals(403, response.status)
        assertTrue("access_denied" in response.contentAsString, response.contentAsString)
    }

    @Test
    fun `a policy that throws refuses`() {
        val (allowed, response) = handled({ _, _ -> error("boom") }, request("GET", PROTECTED, session = true))
        assertFalse(allowed)
        assertEquals(403, response.status)
    }

    @Test
    fun `every operation the session filter lets through without a session is not asked by the policy`() {
        assertTrue(SESSIONLESS_OPERATIONS.isNotEmpty())
        for (operation in SESSIONLESS_OPERATIONS) {
            val (method, uri) = operation.split(" ")
            assertTrue(handled({ _, _ -> false }, request(method, uri, session = false)).first, operation)
        }
    }
}
