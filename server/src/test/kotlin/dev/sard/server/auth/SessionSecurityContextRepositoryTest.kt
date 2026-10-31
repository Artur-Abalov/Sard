// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.auth

import dev.sard.server.api.SESSION_COOKIE
import dev.sard.server.extension.Subject
import dev.sard.server.extension.SubjectAuthentication
import dev.sard.server.pki.MovableClock
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import jakarta.servlet.http.Cookie
import org.springframework.mock.web.MockHttpServletRequest
import org.springframework.mock.web.MockHttpServletResponse
import org.springframework.security.core.Authentication
import org.springframework.security.core.context.SecurityContextImpl
import java.time.Duration
import java.time.Instant
import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertIs
import kotlin.test.assertNotNull
import kotlin.test.assertNull
import kotlin.test.assertTrue

private val T0: Instant = Instant.parse("2026-10-31T12:00:00Z")
private val TENANT: UUID = UUID.fromString("00000000-0000-0000-0000-000000000001")

/**
 * Rules "Цепочка ядра читает сессию репозиторием контекста безопасности" (Р36) and "Продление сессии" (Р52, Р53):
 * the session store as the security context of a request, with no HttpSession.
 */
@MutFlowTest
class SessionSecurityContextRepositoryTest {
    private val clock = MovableClock(T0)
    private val store = SessionStore(clock)
    private val repository = SessionSecurityContextRepository(store)

    private fun authenticationOf(request: MockHttpServletRequest): Authentication? =
        MutFlow.underTest { repository.loadDeferredContext(request).get().authentication }

    private fun request(
        method: String,
        path: String,
        vararg cookies: Cookie,
    ) = MockHttpServletRequest(method, path).apply {
        requestURI = path
        remoteAddr = "10.1.2.3"
        if (cookies.isNotEmpty()) setCookies(*cookies)
    }

    private fun sessionCookie(value: String) = Cookie(SESSION_COOKIE, value)

    @Test
    fun `a valid session cookie gives the authentication of the administrator`() {
        val session = store.create(TENANT)

        val authentication = authenticationOf(request("GET", "/api/v1/sources", sessionCookie(session.id)))

        assertIs<SubjectAuthentication>(authentication)
        assertTrue(authentication.isAuthenticated)
        assertEquals(Subject.ADMINISTRATOR, authentication.subject)
    }

    @Test
    fun `a request without a cookie has no authentication`() {
        assertNull(authenticationOf(request("GET", "/api/v1/sources")))
    }

    @Test
    fun `an unknown, empty or expired cookie gives no authentication`() {
        val expired = store.create(TENANT)
        clock.now = T0.plus(Duration.ofHours(2))

        for (value in listOf("forged-session-id", "", "a".repeat(8192), expired.id)) {
            val path = "/api/v1/sources"
            assertNull(authenticationOf(request("GET", path, sessionCookie(value))), "cookie of ${value.length}")
        }
    }

    @Test
    fun `the first sard_session cookie of the request counts`() {
        val session = store.create(TENANT)
        val valid = sessionCookie(session.id)
        val forged = sessionCookie("forged-session-id")

        assertNotNull(authenticationOf(request("GET", "/api/v1/sources", valid, forged)))
        assertNull(authenticationOf(request("GET", "/api/v1/sources", forged, valid)))
    }

    @Test
    fun `a cookie of another name is not a session`() {
        val session = store.create(TENANT)

        assertNull(authenticationOf(request("GET", "/api/v1/sources", Cookie("SARD_SESSION", session.id))))
    }

    @Test
    fun `a request outside api v1 neither reads nor extends the session`() {
        val session = store.create(TENANT)
        clock.now = T0.plus(Duration.ofMinutes(59))

        assertNull(authenticationOf(request("GET", "/agents", sessionCookie(session.id))))
        assertNull(authenticationOf(request("GET", "/actuator/health", sessionCookie(session.id))))
        assertNull(authenticationOf(request("GET", "/api/v2/agents", sessionCookie(session.id))))

        clock.now = T0.plus(Duration.ofHours(1))
        assertNull(store.find(session.id))
    }

    @Test
    fun `a request under api v1 extends the session by touching it`() {
        val session = store.create(TENANT)
        clock.now = T0.plus(Duration.ofMinutes(59))

        assertNotNull(authenticationOf(request("GET", "/api/v1/onboarding", sessionCookie(session.id))))

        clock.now = T0.plus(Duration.ofHours(1))
        assertNotNull(store.find(session.id))
    }

    @Test
    fun `the touch records the address of the request`() {
        val session = store.create(TENANT, "192.0.2.1")

        authenticationOf(request("GET", "/api/v1/sources", sessionCookie(session.id)))

        assertEquals("10.1.2.3", store.find(session.id)?.clientAddress)
    }

    @Test
    fun `the operations that do not look at a session neither read nor extend it`() {
        val session = store.create(TENANT)
        val cookie = sessionCookie(session.id)
        clock.now = T0.plus(Duration.ofMinutes(59))
        val sessionless =
            listOf(
                "POST" to "/api/v1/session",
                "GET" to "/api/v1/status",
                "POST" to "/api/v1/onboarding/setup-session",
            )

        for ((method, path) in sessionless) {
            assertNull(authenticationOf(request(method, path, cookie)), "$method $path")
        }

        clock.now = T0.plus(Duration.ofHours(1))
        assertNull(store.find(session.id))
    }

    @Test
    fun `a sessionless path with another method or spelling does read the session`() {
        val session = store.create(TENANT)
        val cookie = sessionCookie(session.id)

        val others = listOf("HEAD" to "/api/v1/status", "GET" to "/api/v1/session", "GET" to "/api/v1/STATUS")
        for ((method, path) in others) {
            assertNotNull(authenticationOf(request(method, path, cookie)), "$method $path")
        }
    }

    @Test
    fun `the context is never saved and no repository claims to hold one`() {
        val request = request("GET", "/api/v1/sources")

        val context = SecurityContextImpl(SubjectAuthentication(Subject.ADMINISTRATOR))
        MutFlow.underTest { repository.saveContext(context, request, MockHttpServletResponse()) }
        assertFalse(repository.containsContext(request))
        assertNull(authenticationOf(request))
    }
}
