// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.auth

import dev.sard.server.api.captureLogs
import dev.sard.server.extension.AccessPolicy
import dev.sard.server.extension.Operation
import dev.sard.server.extension.Subject
import dev.sard.server.extension.SubjectAuthentication
import dev.sard.server.extension.SubjectSource
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import org.springframework.beans.factory.ObjectProvider
import org.springframework.beans.factory.support.StaticListableBeanFactory
import org.springframework.mock.web.MockHttpServletRequest
import org.springframework.security.authentication.AnonymousAuthenticationToken
import org.springframework.security.authentication.InsufficientAuthenticationException
import org.springframework.security.authentication.TestingAuthenticationToken
import org.springframework.security.core.Authentication
import org.springframework.security.core.authority.AuthorityUtils
import org.springframework.security.web.access.intercept.RequestAuthorizationContext
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertFalse
import kotlin.test.assertTrue

private val SOURCES = Operation("GET", "/api/v1/sources")
private val ALICE = Subject("sso-user", "alice")

private fun <T : Any> providerOf(
    type: Class<T>,
    bean: T,
): ObjectProvider<T> = StaticListableBeanFactory(mapOf(type.simpleName to bean)).getBeanProvider(type)

/**
 * Rule "Политика доступа спрашивается в цепочке один раз и только об операциях" (Р40, Р30, Р31, Р16): the policy
 * as the authorization manager of the chain, after the authentication; a refusal of the authentication is an
 * AuthenticationException (401), a refusal of the policy is a denied decision (403).
 */
@MutFlowTest
class AccessPolicyAuthorizationManagerTest {
    private class Outcome(
        val granted: Boolean,
        val failure: Exception?,
    )

    private fun decide(
        authentication: Authentication?,
        policy: AccessPolicy = AccessPolicy { _, _ -> true },
        subjects: SubjectSource = SubjectSource { Subject.ADMINISTRATOR },
        operation: Operation? = SOURCES,
    ): Outcome {
        val manager =
            AccessPolicyAuthorizationManager(
                providerOf(AccessPolicy::class.java, policy),
                providerOf(SubjectSource::class.java, subjects),
            ) { operation }
        val context = RequestAuthorizationContext(MockHttpServletRequest("GET", "/api/v1/sources"))
        return try {
            Outcome(MutFlow.underTest { manager.authorize({ authentication }, context) }?.isGranted == true, null)
        } catch (e: InsufficientAuthenticationException) {
            Outcome(false, e)
        }
    }

    private val admin = SubjectAuthentication(Subject.ADMINISTRATOR)

    @Test
    fun `an authenticated request whose operation the policy permits is granted`() {
        assertTrue(decide(admin).granted)
    }

    @Test
    fun `the policy is asked about the subject the source names and the operation`() {
        val asked = mutableListOf<Pair<Subject, Operation>>()

        decide(admin, { subject, operation -> asked.add(subject to operation) }, { ALICE })

        assertEquals(listOf(ALICE to SOURCES), asked)
    }

    @Test
    fun `a request without an authentication is 401 whatever the source and the policy say`() {
        val asked = mutableListOf<Subject>()

        val outcome = decide(null, { subject, _ -> asked.add(subject) }, { ALICE })

        assertTrue(outcome.failure is InsufficientAuthenticationException)
        assertTrue(asked.isEmpty())
    }

    @Test
    fun `an anonymous or not authenticated authentication is 401`() {
        val roles = AuthorityUtils.createAuthorityList("ROLE_ANONYMOUS")
        val anonymous = AnonymousAuthenticationToken("k", "anonymous", roles)
        val notAuthenticated = SubjectAuthentication(Subject.ADMINISTRATOR, authenticated = false)

        for (authentication in listOf(anonymous, notAuthenticated, TestingAuthenticationToken("x", "y"))) {
            assertTrue(decide(authentication).failure is InsufficientAuthenticationException, "$authentication")
        }
    }

    @Test
    fun `an authentication the source does not name a subject for is 401`() {
        val foreign = TestingAuthenticationToken("x", "y").apply { isAuthenticated = true }

        val outcome = decide(foreign, { _, _ -> true }, { null })

        assertTrue(outcome.failure is InsufficientAuthenticationException)
    }

    @Test
    fun `a source that throws refuses with one WARN and the policy is not asked`() {
        val asked = mutableListOf<Subject>()
        lateinit var outcome: Outcome

        val broken = SubjectSource { error("sso unreachable") }

        val logs = captureLogs { outcome = decide(admin, { subject, _ -> asked.add(subject) }, broken) }

        assertFalse(outcome.granted)
        assertEquals(null, outcome.failure)
        assertTrue(asked.isEmpty())
        assertEquals(1, logs.count { "subject source failed" in it }, logs.toString())
    }

    @Test
    fun `a policy that refuses or throws denies`() {
        assertFalse(decide(admin, { _, _ -> false }).granted)
        assertFalse(decide(admin, { _, _ -> error("boom") }).granted)
    }

    @Test
    fun `a request that has no operation is granted without asking the policy`() {
        val asked = mutableListOf<Subject>()

        val outcome = decide(admin, { subject, _ -> asked.add(subject) }, operation = null)

        assertTrue(outcome.granted)
        assertTrue(asked.isEmpty())
    }

    @Test
    fun `the authenticated manager of sign-out grants an authentication and refuses its absence with 401`() {
        val manager = AuthenticatedAuthorizationManager()
        val context = RequestAuthorizationContext(MockHttpServletRequest("DELETE", "/api/v1/session"))

        assertTrue(MutFlow.underTest { manager.authorize({ admin }, context) }?.isGranted == true)
        assertFailsWith<InsufficientAuthenticationException> { manager.authorize({ null }, context) }
    }
}
