// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.auth

import dev.sard.server.api.SESSION_REQUEST_ATTRIBUTE
import dev.sard.server.api.captureLogs
import dev.sard.server.extension.AccessPolicy
import dev.sard.server.extension.Operation
import dev.sard.server.extension.SUBJECT_REQUEST_ATTRIBUTE
import dev.sard.server.extension.Subject
import dev.sard.server.extension.SubjectSource
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
    private fun interceptor(
        policy: AccessPolicy,
        subjects: SubjectSource,
    ): AccessPolicyInterceptor {
        val mapper = providerOf(ObjectMapper::class.java, ObjectMapper())
        return AccessPolicyInterceptor(
            providerOf(AccessPolicy::class.java, policy),
            providerOf(SubjectSource::class.java, subjects),
            mapper,
        )
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
        subjects: SubjectSource = SubjectSource { Subject.ADMINISTRATOR },
    ): Pair<Boolean, MockHttpServletResponse> {
        val response = MockHttpServletResponse()
        val allowed = MutFlow.underTest { interceptor(policy, subjects).preHandle(request, response, Any()) }
        return allowed to response
    }

    @Test
    fun `a replaced session filter passes its own subject to the policy`() {
        val asked = mutableListOf<Pair<Subject, Operation>>()
        val request =
            request("GET", PROTECTED, session = false).apply {
                setAttribute(SUBJECT_REQUEST_ATTRIBUTE, Subject("sso-user", "alice"))
            }

        val alice = SubjectSource { Subject("sso-user", "alice") }
        val (allowed, response) = handled({ subject, operation -> asked.add(subject to operation) }, request, alice)

        assertTrue(allowed)
        assertEquals(200, response.status)
        assertEquals(listOf(Subject("sso-user", "alice") to Operation("GET", PROTECTED)), asked)
    }

    @Test
    fun `the policy is asked about the subject the source names, not the one of the session`() {
        val asked = mutableListOf<Subject>()
        val robot = SubjectSource { Subject("svc", "robot") }

        val request = request("GET", PROTECTED, session = true)

        val (allowed, _) = handled({ subject, _ -> asked.add(subject) }, request, robot)

        assertTrue(allowed)
        assertEquals(listOf(Subject("svc", "robot")), asked)
    }

    @Test
    fun `a source that names a subject does not authenticate a request without a core session`() {
        val asked = mutableListOf<Subject>()
        val robot = SubjectSource { Subject("svc", "robot") }

        val (allowed, response) =
            handled({ subject, _ -> asked.add(subject) }, request("GET", PROTECTED, session = false), robot)

        assertFalse(allowed)
        assertEquals(401, response.status)
        assertTrue(asked.isEmpty())
    }

    @Test
    fun `a source that names nobody closes the operation with 401`() {
        val (allowed, response) =
            handled({ _, _ -> true }, request("GET", PROTECTED, session = true), SubjectSource { null })

        assertFalse(allowed)
        assertEquals(401, response.status)
        assertTrue("unauthenticated" in response.contentAsString, response.contentAsString)
    }

    @Test
    fun `a source that throws closes the operation with 403 access_denied and one WARN, the policy is not asked`() {
        val asked = mutableListOf<Subject>()
        val broken = SubjectSource { error("sso unreachable") }
        lateinit var outcome: Pair<Boolean, MockHttpServletResponse>

        val logs =
            captureLogs {
                val request = request("GET", PROTECTED, session = true)
                outcome = handled({ subject, _ -> asked.add(subject) }, request, broken)
            }

        assertFalse(outcome.first)
        assertEquals(403, outcome.second.status)
        assertTrue("access_denied" in outcome.second.contentAsString, outcome.second.contentAsString)
        assertTrue(asked.isEmpty())
        assertEquals(1, logs.count { "subject source failed" in it }, logs.toString())
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
