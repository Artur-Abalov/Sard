// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import dev.sard.server.enrollment.Enrollment
import dev.sard.server.enrollment.EnrollmentTokens
import dev.sard.server.extension.EventLog
import dev.sard.server.extension.FixturePolicy
import dev.sard.server.extension.FixturePolicyConfiguration
import dev.sard.server.extension.Operation
import dev.sard.server.extension.RecordingConfiguration
import dev.sard.server.extension.SardSecurityCustomizer
import dev.sard.server.extension.Subject
import dev.sard.server.extension.SubjectAuthentication
import dev.sard.server.pki.CertificateAuthority
import dev.sard.server.pki.MovableClock
import dev.sard.server.registration.Registration
import dev.sard.testext.security.OpenExtController
import jakarta.servlet.FilterChain
import jakarta.servlet.http.HttpServletRequest
import jakarta.servlet.http.HttpServletResponse
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.beans.factory.annotation.Value
import org.springframework.boot.test.context.TestConfiguration
import org.springframework.boot.test.web.server.LocalServerPort
import org.springframework.context.annotation.Bean
import org.springframework.context.annotation.Import
import org.springframework.jdbc.core.JdbcTemplate
import org.springframework.security.authentication.TestingAuthenticationToken
import org.springframework.security.config.annotation.web.builders.HttpSecurity
import org.springframework.security.core.context.SecurityContextHolder
import org.springframework.security.web.access.intercept.AuthorizationFilter
import org.springframework.web.filter.OncePerRequestFilter
import tools.jackson.databind.ObjectMapper
import kotlin.test.AfterTest
import kotlin.test.BeforeTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

private val ALICE = Subject("sso-user", "alice")
private const val OPEN_START = "/api/v1/open-ext/start"

/**
 * Stands in for the filter of an SSO extension: the header X-Test-Auth names the authentication it puts into the
 * security context (subj, foreign, unauth); X-Test-Attribute puts the subject under the removed request attribute.
 */
private class AuthTestFilter : OncePerRequestFilter() {
    override fun doFilterInternal(
        request: HttpServletRequest,
        response: HttpServletResponse,
        chain: FilterChain,
    ) {
        val context = SecurityContextHolder.getContext()
        when (request.getHeader("X-Test-Auth")) {
            "subj" -> context.authentication = SubjectAuthentication(ALICE)
            "foreign" -> context.authentication = TestingAuthenticationToken("x", "y", "ROLE_X")
            "unauth" -> context.authentication = SubjectAuthentication(ALICE, authenticated = false)
        }
        if (request.getHeader("X-Test-Attribute") != null) request.setAttribute("dev.sard.server.subject", ALICE)
        chain.doFilter(request, response)
    }
}

/** A test extension "open-ext": a filter that authenticates by header, and one public operation. */
@TestConfiguration(proxyBeanMethods = false)
class AuthExtensionConfiguration {
    @Bean("open-ext")
    fun openExt(): SardSecurityCustomizer =
        object : SardSecurityCustomizer {
            override val publicOperations = setOf(Operation("GET", OPEN_START))

            override fun customize(http: HttpSecurity) {
                http.addFilterBefore(AuthTestFilter(), AuthorizationFilter::class.java)
            }
        }

    @Bean
    fun openExtController() = OpenExtController()
}

/** Rule "Расширение встраивает вход через настройщик цепочки ядра" (spring-security.feature, Р39, Р41). */
@RestApiTest
@Import(FixturePolicyConfiguration::class, RecordingConfiguration::class, AuthExtensionConfiguration::class)
class SecurityExtensionApiIntegrationTest(
    @Autowired ca: CertificateAuthority,
    @Autowired enrollment: Enrollment,
    @Autowired tokens: EnrollmentTokens,
    @Autowired registration: Registration,
    @Autowired jdbc: JdbcTemplate,
    @Autowired clock: MovableClock,
    @Autowired private val mapper: ObjectMapper,
    @Autowired private val policy: FixturePolicy,
    @Autowired private val log: EventLog,
    @LocalServerPort port: Int,
    @Value("\${local.grpc.server.port}") grpc: Int,
) {
    private val world = RestWorld(ca, enrollment, tokens, registration, jdbc, clock, mapper, port, grpc)
    private val api = world.api

    @BeforeTest
    fun `the policy and the events start clean`() {
        policy.reset()
        log.clear()
    }

    @AfterTest
    fun `drop the tenants`() {
        policy.reset()
        world.close()
    }

    private fun anonymous(
        method: String,
        path: String,
        vararg headers: Pair<String, String>,
        body: String? = null,
    ) = api.sendUnchecked(method, path, null, body, headers.toMap())

    @Test
    fun `Аутентификация расширения с субъектом доходит до политики и до actor`() {
        val body = """{"backupTimeoutMinutes":481,"restoreTimeoutMinutes":601}"""

        val response = anonymous("PUT", "/api/v1/run-defaults", "X-Test-Auth" to "subj", body = body)

        assertEquals(200, response.status, response.toString())
        assertEquals(listOf(ALICE to Operation("PUT", "/api/v1/run-defaults")), policy.calls)
        val changed = log.settings("run_defaults").single()
        assertEquals(ALICE, changed.actor)
    }

    @Test
    fun `Аутентификация расширения, которую источник субъекта не называет, отвечает 401`() {
        val response = anonymous("GET", "/api/v1/sources", "X-Test-Auth" to "foreign")

        assertEquals(401, response.status)
        assertEquals("unauthenticated", response.code)
        assertTrue(policy.calls.isEmpty())
    }

    @Test
    fun `Неаутентифицированная аутентификация расширения отвечает 401`() {
        val response = anonymous("GET", "/api/v1/sources", "X-Test-Auth" to "unauth")

        assertEquals(401, response.status)
        assertEquals("unauthenticated", response.code)
    }

    @Test
    fun `Субъект под атрибутом запроса больше ничего не открывает`() {
        val response = anonymous("GET", "/api/v1/sources", "X-Test-Attribute" to "1")

        assertEquals(401, response.status)
        assertEquals("unauthenticated", response.code)
    }

    @Test
    fun `Публичная операция расширения открыта без сессии и не спрашивает политику`() {
        policy.denyAll = true

        val response = anonymous("GET", OPEN_START)

        assertEquals(200, response.status, response.toString())
        assertEquals("started", response.body)
        assertTrue(policy.calls.isEmpty())
    }

    @Test
    fun `Публичная операция расширения не открывает соседние пути`() {
        val response = anonymous("GET", "$OPEN_START/deeper")

        assertEquals(401, response.status)
        assertEquals("unauthenticated", response.code)
    }

    @Test
    fun `Публичная операция расширения описана в OpenAPI с пустым требованием безопасности`() {
        val document = mapper.readTree(anonymous("GET", "/v3/api-docs").body)

        val open =
            document
                .path("paths")
                .properties()
                .flatMap { (path, item) ->
                    item.properties().map { (method, op) -> "${method.uppercase()} $path" to op }
                }.filter { (_, op) -> op.path("security").let { it.isArray && it.isEmpty } }
                .map { it.first }
                .toSet()
        val expected =
            setOf(
                "POST /api/v1/session",
                "GET /api/v1/status",
                "GET /api/v1/onboarding",
                "POST /api/v1/onboarding/setup-session",
                "GET $OPEN_START",
            )
        assertEquals(expected, open)
    }
}
