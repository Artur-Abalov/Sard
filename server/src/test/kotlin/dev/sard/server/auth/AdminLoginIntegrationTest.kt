// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.auth

import dev.sard.server.TestcontainersConfiguration
import dev.sard.server.api.SESSION_COOKIE
import dev.sard.server.pki.MovableClock
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.boot.test.context.TestConfiguration
import org.springframework.boot.test.web.server.LocalServerPort
import org.springframework.context.annotation.Bean
import org.springframework.context.annotation.Import
import tools.jackson.databind.JsonNode
import tools.jackson.databind.ObjectMapper
import java.net.URI
import java.net.http.HttpClient
import java.net.http.HttpRequest
import java.net.http.HttpResponse
import java.time.Clock
import java.time.Duration
import java.time.Instant
import java.time.ZoneOffset
import java.util.concurrent.CountDownLatch
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit
import kotlin.test.AfterTest
import kotlin.test.BeforeTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNotEquals
import kotlin.test.assertNotNull
import kotlin.test.assertNull
import kotlin.test.assertTrue

private val LOGIN: Instant = Instant.parse("2026-10-01T12:00:00Z")
private const val PASSWORD = "correct-horse-battery"
private const val TENANT_ID = "00000000-0000-0000-0000-000000000001"

@TestConfiguration(proxyBeanMethods = false)
class AdminLoginClockConfiguration {
    @Bean
    fun clock(): Clock = MovableClock(LOGIN)
}

/**
 * W1b: sign-in, current session, sign-out, brute-force lock and CSRF, end to end
 * (docs/specs/server/admin-login.feature, @http). Each test gets a fresh session store
 * and attempt tracker (request-scoped beans recreated on context reuse would leak state
 * between tests otherwise), so the class disables Spring's context cache for itself.
 */
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.RANDOM_PORT,
    properties = ["spring.grpc.server.port=0", "SARD_ADMIN_PASSWORD=$PASSWORD"],
)
@Import(TestcontainersConfiguration::class, AdminLoginClockConfiguration::class)
class AdminLoginIntegrationTest(
    @Autowired private val mapper: ObjectMapper,
    @Autowired private val clock: Clock,
    @Autowired private val sessionStore: SessionStore,
    @Autowired private val attemptTracker: LoginAttemptTracker,
    @LocalServerPort private val port: Int,
) {
    private val http = HttpClient.newHttpClient()
    private val movable get() = clock as MovableClock

    @BeforeTest
    fun `reset the clock and forget every session and lock`() {
        movable.now = LOGIN
        sessionStore.forgetEverythingForTests()
        attemptTracker.forgetEverythingForTests()
    }

    @AfterTest
    fun `forget every session and lock again`() {
        sessionStore.forgetEverythingForTests()
        attemptTracker.forgetEverythingForTests()
    }

    private fun send(
        method: String,
        path: String,
        body: String? = null,
        cookie: String? = null,
        origin: String? = null,
        host: String? = null,
    ): HttpResponse<String> {
        val publisher = body?.let { HttpRequest.BodyPublishers.ofString(it) } ?: HttpRequest.BodyPublishers.noBody()
        val builder =
            HttpRequest
                .newBuilder(URI.create("http://localhost:$port$path"))
                .header("Content-Type", "application/json")
                .method(method, publisher)
        cookie?.let { builder.header("Cookie", "$SESSION_COOKIE=$it") }
        origin?.let { builder.header("Origin", it) }
        host?.let { builder.header("Host", it) }
        return http.send(builder.build(), HttpResponse.BodyHandlers.ofString())
    }

    private fun login(
        password: String = PASSWORD,
        cookie: String? = null,
        origin: String? = null,
    ): HttpResponse<String> = send("POST", "/api/v1/session", """{"password":"$password"}""", cookie, origin)

    private fun sessionIdOf(response: HttpResponse<String>): String {
        val setCookie = response.headers().firstValue("Set-Cookie").orElseThrow()
        return Regex("$SESSION_COOKIE=([^;]*)").find(setCookie)!!.groupValues[1]
    }

    private fun signIn(): String = sessionIdOf(login())

    private fun body(response: HttpResponse<String>): JsonNode = mapper.readTree(response.body())

    // ---- Правило: Верный пароль выдаёт серверную сессию в cookie sard_session ----

    @Test
    fun `a correct password answers 204 and sets a non-empty session cookie`() {
        val response = login()
        assertEquals(204, response.statusCode())
        assertTrue(response.body().isEmpty())
        val setCookie = response.headers().firstValue("Set-Cookie").orElseThrow()
        assertTrue(setCookie.contains("$SESSION_COOKIE="))
        assertTrue(sessionIdOf(response).isNotEmpty())
    }

    @Test
    fun `the session cookie is HttpOnly, SameSite=Strict and Path root`() {
        val setCookie = login().headers().firstValue("Set-Cookie").orElseThrow()
        assertTrue(setCookie.contains("HttpOnly"), setCookie)
        assertTrue(setCookie.contains("SameSite=Strict"), setCookie)
        assertTrue(setCookie.contains("Path=/"), setCookie)
    }

    @Test
    fun `the session cookie over HTTP is not marked Secure`() {
        val setCookie = login().headers().firstValue("Set-Cookie").orElseThrow()
        assertTrue(!setCookie.contains("Secure"), setCookie)
    }

    @Test
    fun `the session cookie sets no Max-Age or Expires`() {
        val setCookie = login().headers().firstValue("Set-Cookie").orElseThrow()
        assertTrue(!setCookie.contains("Max-Age"), setCookie)
        assertTrue(!setCookie.contains("Expires"), setCookie)
    }

    @Test
    fun `the current session names the default tenant and its idle deadline`() {
        val cookie = signIn()
        val response = send("GET", "/api/v1/session", cookie = cookie)
        assertEquals(200, response.statusCode())
        assertEquals(TENANT_ID, body(response).path("tenantId").asString())
        assertEquals("2026-10-02T00:00:00Z", body(response).path("expiresAt").asString())
    }

    @Test
    fun `the current session without a cookie is 401 unauthenticated`() {
        val response = send("GET", "/api/v1/session")
        assertEquals(401, response.statusCode())
        assertEquals("unauthenticated", body(response).path("code").asString())
    }

    @Test
    fun `an unknown session id is 401`() {
        val response = send("GET", "/api/v1/session", cookie = "forged-session-id")
        assertEquals(401, response.statusCode())
    }

    @Test
    fun `signing in again with a valid session issues a different id and the old one stops working`() {
        val s1 = signIn()
        val second = login(cookie = s1)
        assertEquals(204, second.statusCode())
        val s2 = sessionIdOf(second)
        assertNotEquals(s1, s2)
        assertEquals(401, send("GET", "/api/v1/session", cookie = s1).statusCode())
        assertEquals(200, send("GET", "/api/v1/session", cookie = s2).statusCode())
    }

    @Test
    fun `a session id sent by the client before signing in is not honored`() {
        val response = login(cookie = "attacker-chosen")
        assertNotEquals("attacker-chosen", sessionIdOf(response))
        assertEquals(401, send("GET", "/api/v1/session", cookie = "attacker-chosen").statusCode())
    }

    @Test
    fun `two sign-ins give two sessions valid at once with different ids`() {
        val s1 = signIn()
        val s2 = signIn()
        assertNotEquals(s1, s2)
        assertEquals(200, send("GET", "/api/v1/session", cookie = s1).statusCode())
        assertEquals(200, send("GET", "/api/v1/session", cookie = s2).statusCode())
    }

    @Test
    fun `a different case password is not accepted`() {
        assertEquals(401, login("Correct-Horse-Battery").statusCode())
    }

    @Test
    fun `a trailing space in the password is not accepted`() {
        assertEquals(401, login("correct-horse-battery ").statusCode())
    }

    // ---- Правило: Неверный пароль отклоняется одним и тем же ответом ----

    @Test
    fun `a wrong password answers 401 without a cookie`() {
        val response = login("wrong-password-123")
        assertEquals(401, response.statusCode())
        assertTrue(response.headers().firstValue("Set-Cookie").isEmpty)
    }

    @Test
    fun `the answer to a wrong password does not depend on why it is wrong`() {
        val reference = login("wrong-password-123")
        val referenceBody = reference.body()
        val candidates =
            listOf("", "correct-horse-batter", "correct-horse-battery!", "CORRECT-HORSE-BATTERY", "a".repeat(10_000))
        for (password in candidates) {
            // Isolate each comparison from the brute-force lock (a separate rule): only
            // the single attempt below counts as a failure for its address.
            attemptTracker.forgetEverythingForTests()
            val response = login(password)
            val referenceContentType = reference.headers().firstValue("Content-Type")
            assertEquals(reference.statusCode(), response.statusCode(), password)
            assertEquals(referenceContentType, response.headers().firstValue("Content-Type"), password)
            assertEquals(referenceBody, response.body(), password)
        }
    }

    @Test
    fun `a sign-in request without a usable password answers as a wrong password`() {
        for (rawBody in listOf("{}", """{"password": null}""", "password=x", "")) {
            val response = send("POST", "/api/v1/session", rawBody)
            assertEquals(401, response.statusCode(), rawBody)
            assertEquals("unauthenticated", body(response).path("code").asString(), rawBody)
        }
    }

    // ---- Правило: Сессия завершается после 12 часов бездействия и не позже 7 дней после входа ----

    @Test
    fun `a session a millisecond before 12 hours idle is still valid`() {
        val cookie = signIn()
        movable.now = LOGIN + Duration.ofHours(12) - Duration.ofMillis(1)
        assertEquals(200, send("GET", "/api/v1/session", cookie = cookie).statusCode())
    }

    @Test
    fun `a session exactly 12 hours idle is no longer valid`() {
        val cookie = signIn()
        movable.now = LOGIN + Duration.ofHours(12)
        assertEquals(401, send("GET", "/api/v1/session", cookie = cookie).statusCode())
    }

    @Test
    fun `a request with the session extends its idle deadline`() {
        val cookie = signIn()
        movable.now = LOGIN + Duration.ofHours(11)
        send("GET", "/api/v1/agents", cookie = cookie)
        movable.now = LOGIN + Duration.ofHours(22) + Duration.ofHours(23 - 22) - Duration.ofMillis(1)
        // 2026-10-02T10:59:59.999Z, a millisecond before 12h past the 23:00 touch.
        movable.now = Instant.parse("2026-10-02T10:59:59.999Z")
        val response = send("GET", "/api/v1/session", cookie = cookie)
        assertEquals(200, response.statusCode())
        assertEquals("2026-10-02T22:59:59.999Z", body(response).path("expiresAt").asString())
    }

    @Test
    fun `a request that gets 200 from a stage 1 endpoint also extends the idle deadline`() {
        val cookie = signIn()
        movable.now = LOGIN + Duration.ofHours(11)
        val endpoint = send("GET", "/api/v1/agents", cookie = cookie)
        assertEquals(200, endpoint.statusCode())
        movable.now = LOGIN + Duration.ofHours(22) + Duration.ofHours(1) - Duration.ofMillis(1)
        assertEquals(200, send("GET", "/api/v1/session", cookie = cookie).statusCode())
    }

    @Test
    fun `an active session a millisecond before 7 days after login is still valid`() {
        val cookie = signIn()
        var now = LOGIN
        while (now + Duration.ofHours(11) < LOGIN + Duration.ofDays(7)) {
            now += Duration.ofHours(11)
            movable.now = now
            send("GET", "/api/v1/agents", cookie = cookie)
        }
        movable.now = LOGIN + Duration.ofDays(7) - Duration.ofMillis(1)
        assertEquals(200, send("GET", "/api/v1/session", cookie = cookie).statusCode())
    }

    @Test
    fun `an active session exactly 7 days after login is no longer valid`() {
        val cookie = signIn()
        var now = LOGIN
        while (now + Duration.ofHours(11) < LOGIN + Duration.ofDays(7)) {
            now += Duration.ofHours(11)
            movable.now = now
            send("GET", "/api/v1/agents", cookie = cookie)
        }
        movable.now = LOGIN + Duration.ofDays(7)
        assertEquals(401, send("GET", "/api/v1/session", cookie = cookie).statusCode())
    }

    @Test
    fun `restarting the store ends every session`() {
        val cookie = signIn()
        sessionStore.forgetEverythingForTests()
        assertEquals(401, send("GET", "/api/v1/session", cookie = cookie).statusCode())
    }

    // ---- Правило: Выход завершает сессию и очищает cookie ----

    @Test
    fun `signing out answers 204 and clears the cookie`() {
        val cookie = signIn()
        val response = send("DELETE", "/api/v1/session", cookie = cookie)
        assertEquals(204, response.statusCode())
        val setCookie = response.headers().firstValue("Set-Cookie").orElseThrow()
        assertTrue(setCookie.contains("$SESSION_COOKIE="), setCookie)
        assertTrue(setCookie.contains("Max-Age=0"), setCookie)
        assertTrue(setCookie.contains("Path=/"), setCookie)
    }

    @Test
    fun `after sign-out the old session id no longer works`() {
        val cookie = signIn()
        send("DELETE", "/api/v1/session", cookie = cookie)
        assertEquals(401, send("GET", "/api/v1/session", cookie = cookie).statusCode())
    }

    @Test
    fun `signing out without a cookie is 401`() {
        assertEquals(401, send("DELETE", "/api/v1/session").statusCode())
    }

    @Test
    fun `signing out twice with the same id is 401 the second time`() {
        val cookie = signIn()
        send("DELETE", "/api/v1/session", cookie = cookie)
        assertEquals(401, send("DELETE", "/api/v1/session", cookie = cookie).statusCode())
    }

    @Test
    fun `signing out with an invalid session still clears the cookie (Р12)`() {
        val response = send("DELETE", "/api/v1/session", cookie = "forged-session-id")
        assertEquals(401, response.statusCode())
        val setCookie = response.headers().firstValue("Set-Cookie").orElseThrow()
        assertTrue(setCookie.contains("Max-Age=0"), setCookie)
    }

    @Test
    fun `an unauthenticated GET does not clear the cookie (only DELETE does, Р12)`() {
        val response = send("GET", "/api/v1/session", cookie = "forged-session-id")
        assertEquals(401, response.statusCode())
        assertTrue(response.headers().firstValue("Set-Cookie").isEmpty)
    }

    @Test
    fun `signing out ends only its own session`() {
        val s1 = signIn()
        val s2 = signIn()
        send("DELETE", "/api/v1/session", cookie = s1)
        assertEquals(200, send("GET", "/api/v1/session", cookie = s2).statusCode())
    }

    // ---- Правило: После 5 неудач с одного адреса за 15 минут вход с него отклоняется ----

    @Test
    fun `the fifth failed attempt still answers 401`() {
        repeat(4) { login("wrong-password-123") }
        assertEquals(401, login("wrong-password-123").statusCode())
    }

    @Test
    fun `the sixth attempt after five failures is 429 with Retry-After`() {
        repeat(5) { login("wrong-password-123") }
        val response = login("wrong-password-123")
        assertEquals(429, response.statusCode())
        assertEquals("900", response.headers().firstValue("Retry-After").orElse(""))
    }

    @Test
    fun `the correct password during the lock is still 429, no cookie`() {
        repeat(5) { login("wrong-password-123") }
        val response = login()
        assertEquals(429, response.statusCode())
        assertTrue(response.headers().firstValue("Set-Cookie").isEmpty)
    }

    @Test
    fun `the locked answer does not depend on whether the password is right`() {
        repeat(5) { login("wrong-password-123") }
        val reference = login("wrong-password-123")
        val response = login()
        assertEquals(reference.statusCode(), response.statusCode())
        assertEquals(reference.body(), response.body())
    }

    @Test
    fun `Retry-After counts down from the fifth failure`() {
        repeat(5) { login("wrong-password-123") }
        movable.now = LOGIN + Duration.ofMinutes(10)
        val response = login()
        assertEquals(429, response.statusCode())
        assertEquals("300", response.headers().firstValue("Retry-After").orElse(""))
    }

    @Test
    fun `Retry-After rounds up to a whole second`() {
        repeat(5) { login("wrong-password-123") }
        movable.now = LOGIN + Duration.ofMinutes(14) + Duration.ofSeconds(59) + Duration.ofMillis(999)
        assertEquals("1", login().headers().firstValue("Retry-After").orElse(""))
    }

    @Test
    fun `15 minutes after the fifth failure sign-in works again`() {
        repeat(5) { login("wrong-password-123") }
        movable.now = LOGIN + Duration.ofMinutes(15)
        assertEquals(204, login().statusCode())
    }

    @Test
    fun `attempts during the lock do not extend it`() {
        repeat(5) { login("wrong-password-123") }
        for (minutes in listOf(1L, 5L, 14L)) {
            movable.now = LOGIN + Duration.ofMinutes(minutes)
            assertEquals(429, login("wrong-password-123").statusCode())
        }
        movable.now = LOGIN + Duration.ofMinutes(15)
        assertEquals(204, login().statusCode())
    }

    @Test
    fun `failures older than 15 minutes drop out of the window`() {
        repeat(4) { login("wrong-password-123") }
        movable.now = LOGIN + Duration.ofMinutes(15)
        repeat(4) { login("wrong-password-123") }
        assertEquals(401, login("wrong-password-123").statusCode())
    }

    @Test
    fun `a success clears the address's failure counter`() {
        repeat(4) { login("wrong-password-123") }
        login()
        repeat(4) { login("wrong-password-123") }
        assertEquals(401, login("wrong-password-123").statusCode())
    }

    @Test
    fun `the X-Forwarded-For header does not bypass the lock`() {
        repeat(5) { login("wrong-password-123") }
        val response =
            send("POST", "/api/v1/session", """{"password":"$PASSWORD"}""").let {
                HttpRequest
                    .newBuilder(URI.create("http://localhost:$port/api/v1/session"))
                    .header("Content-Type", "application/json")
                    .header("X-Forwarded-For", "192.0.2.99")
                    .POST(HttpRequest.BodyPublishers.ofString("""{"password":"$PASSWORD"}"""))
                    .build()
                    .let { request -> http.send(request, HttpResponse.BodyHandlers.ofString()) }
            }
        assertEquals(429, response.statusCode())
    }

    @Test
    fun `concurrent wrong attempts never allow more than 5 to see 401`() {
        val start = CountDownLatch(1)
        val pool = Executors.newFixedThreadPool(20)
        val results =
            (1..20)
                .map {
                    pool.submit<Int> {
                        start.await()
                        login("wrong-password-123").statusCode()
                    }
                }.also { start.countDown() }
                .map { it.get(20, TimeUnit.SECONDS) }
        pool.shutdown()
        assertTrue(results.count { it == 401 } <= 5, "$results")
        assertTrue(results.all { it == 401 || it == 429 }, "$results")
    }

    @Test
    fun `locking an address does not end a session already issued from it`() {
        val cookie = signIn()
        repeat(5) { login("wrong-password-123") }
        assertEquals(200, send("GET", "/api/v1/session", cookie = cookie).statusCode())
    }

    // ---- Правило: Изменяющие запросы с чужим Origin отклоняются ----

    @Test
    fun `signing out with a foreign Origin does not end the session`() {
        val cookie = signIn()
        val response = send("DELETE", "/api/v1/session", cookie = cookie, origin = "https://evil.example")
        assertEquals(403, response.statusCode())
        assertEquals("origin_rejected", body(response).path("code").asString())
        assertEquals(200, send("GET", "/api/v1/session", cookie = cookie).statusCode())
    }

    @Test
    fun `signing in with a foreign Origin does not issue a cookie`() {
        val response = login(origin = "https://evil.example")
        assertEquals(403, response.statusCode())
        assertTrue(response.headers().firstValue("Set-Cookie").isEmpty)
    }

    @Test
    fun `a sign-in refused for its Origin does not count as a failed attempt`() {
        repeat(4) { login("wrong-password-123") }
        repeat(3) { assertEquals(403, login("wrong-password-123", origin = "https://evil.example").statusCode()) }
        assertEquals(401, login("wrong-password-123").statusCode())
    }

    @Test
    fun `a reading request with a foreign Origin is not rejected`() {
        val cookie = signIn()
        assertEquals(200, send("GET", "/api/v1/session", cookie = cookie, origin = "https://evil.example").statusCode())
    }

    /**
     * The Origin check runs before the handler is even chosen (rule "Проверка выполняется до
     * выбора обработчика"): a mutating request is 403 origin_rejected, not 401, even without
     * a session and even for a PATCH the contract does not map anywhere.
     */
    @Test
    fun `a mutating request with a foreign Origin is 403 even without a session`() {
        val id = "0192f7a0-0000-7000-8000-000000000201"
        for ((method, path) in mutatingOriginExamples(id)) {
            val response = send(method, path, body = "{}", origin = "https://evil.example")
            assertEquals(403, response.statusCode(), "$method $path")
            assertEquals("origin_rejected", body(response).path("code").asString(), "$method $path")
        }
    }

    @Test
    fun `a mutating request with a session and a foreign Origin is 403 origin_rejected`() {
        val cookie = signIn()
        val id = "0192f7a0-0000-7000-8000-000000000201"
        for ((method, path) in mutatingOriginExamples(id)) {
            val response = send(method, path, body = "{}", cookie = cookie, origin = "https://evil.example")
            assertEquals(403, response.statusCode(), "$method $path")
            assertEquals("origin_rejected", body(response).path("code").asString(), "$method $path")
        }
    }

    /** The spec's own scenario outline examples for "Изменяющий запрос с сессией и чужим Origin отклоняется". */
    private fun mutatingOriginExamples(sourceId: String): List<Pair<String, String>> =
        listOf(
            "POST" to "/api/v1/sources",
            "PUT" to "/api/v1/sources/$sourceId",
            "PATCH" to "/api/v1/sources/$sourceId",
            "DELETE" to "/api/v1/sources/$sourceId",
            "POST" to "/api/v1/enrollment-tokens",
            "POST" to "/api/v1/enrollment-tokens/0192f7a0-0000-7000-8000-000000000501/revoke",
            "POST" to "/api/v1/sources/$sourceId/runs",
            "DELETE" to "/api/v1/session",
        )

    // ---- Правило: Всё API под /api/v1 требует сессию, кроме входа и статуса ----

    @Test
    fun `every operation of the exported spec but sign-in and status is 401 without a session`() {
        val spec = mapper.readTree(send("GET", "/v3/api-docs").body())
        val id = "0192f7a0-0000-7000-8000-000000000001"
        // The filter's own public set (PUBLIC_OPERATIONS, internal) must match the spec's, not a
        // hardcoded list here: a public operation the spec adds without the filter noticing it
        // would otherwise pass this test by accident.
        val public = publicOperationsOf(spec)
        assertEquals(PUBLIC_OPERATIONS, public)
        for ((path, item) in spec.path("paths").properties()) {
            for ((method, _) in item.properties()) {
                val key = "${method.uppercase()} $path"
                if (key in public) continue
                val concretePath = path.replace(Regex("\\{[^}]+}"), id)
                val response = send(method.uppercase(), concretePath)
                assertEquals(401, response.statusCode(), key)
                assertEquals("application/problem+json", response.headers().firstValue("Content-Type").orElse(""), key)
                assertEquals("unauthenticated", body(response).path("code").asString(), key)
            }
        }
    }

    /** Operations the exported OpenAPI declares an empty security requirement for. */
    private fun publicOperationsOf(spec: JsonNode): Set<String> =
        spec
            .path("paths")
            .properties()
            .flatMap { (path, item) ->
                item.properties().mapNotNull { (method, op) ->
                    val security = op.path("security")
                    if (security.isArray && security.isEmpty) "${method.uppercase()} $path" else null
                }
            }.toSet()

    @Test
    fun `an unmapped path under api v1 without a session is 401`() {
        assertEquals(401, send("GET", "/api/v1/no-such-resource").statusCode())
    }

    @Test
    fun `status is open without a session`() {
        assertEquals(200, send("GET", "/api/v1/status").statusCode())
    }

    @Test
    fun `a stage 1 endpoint answers 200 with a session`() {
        val cookie = signIn()
        val response = send("GET", "/api/v1/agents", cookie = cookie)
        assertEquals(200, response.statusCode())
    }

    @Test
    fun `health is open without a session`() {
        val response = send("GET", "/actuator/health")
        assertEquals(200, response.statusCode())
        assertTrue(response.body().contains("UP"))
    }

    @Test
    fun `the OpenAPI document is open without a session`() {
        assertEquals(200, send("GET", "/v3/api-docs").statusCode())
    }

    @Test
    fun `unexposed actuator endpoints are 404`() {
        val unexposed =
            listOf(
                "/actuator/env",
                "/actuator/configprops",
                "/actuator/httpexchanges",
                "/actuator/metrics",
                "/actuator/heapdump",
            )
        for (path in unexposed) {
            assertEquals(404, send("GET", path).statusCode(), path)
        }
    }

    // ---- Password never leaks ----

    @Test
    fun `the wrong password is not echoed in the response`() {
        val response = login("wrong-but-close-password")
        assertTrue(!response.body().contains("wrong-but-close-password"))
        assertTrue(
            response
                .headers()
                .map()
                .values
                .flatten()
                .none { it.contains("wrong-but-close-password") },
        )
    }
}

/** Test-only escape hatches so each test starts from a clean slate without a fresh context. */
fun SessionStore.forgetEverythingForTests() {
    val field = SessionStore::class.java.getDeclaredField("sessions")
    field.isAccessible = true
    (field.get(this) as MutableMap<*, *>).clear()
}

fun LoginAttemptTracker.forgetEverythingForTests() {
    val field = LoginAttemptTracker::class.java.getDeclaredField("byAddress")
    field.isAccessible = true
    (field.get(this) as MutableMap<*, *>).clear()
}
