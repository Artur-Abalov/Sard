// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.auth

import dev.sard.server.SeededAdministrator
import dev.sard.server.TestcontainersConfiguration
import dev.sard.server.api.SESSION_COOKIE
import dev.sard.server.console.FIXTURE_CONSOLE
import dev.sard.server.pki.MovableClock
import jakarta.servlet.http.HttpSessionEvent
import jakarta.servlet.http.HttpSessionListener
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.boot.test.context.TestConfiguration
import org.springframework.boot.test.web.server.LocalServerPort
import org.springframework.boot.web.servlet.ServletListenerRegistrationBean
import org.springframework.context.annotation.Bean
import org.springframework.context.annotation.Import
import tools.jackson.databind.ObjectMapper
import java.net.URI
import java.net.http.HttpClient
import java.net.http.HttpRequest
import java.net.http.HttpResponse
import java.time.Clock
import java.time.Duration
import java.time.Instant
import java.util.Base64
import java.util.concurrent.Callable
import java.util.concurrent.Executors
import java.util.concurrent.atomic.AtomicInteger
import kotlin.test.AfterTest
import kotlin.test.BeforeTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNotNull
import kotlin.test.assertNull
import kotlin.test.assertTrue

private val T0: Instant = Instant.parse("2026-10-31T12:00:00Z")
private const val PASSWORD = "correct-horse-battery"
private val FOREIGN_ORIGIN = mapOf("Origin" to "https://evil.example")
private val API_HEADERS = listOf("X-Content-Type-Options" to "nosniff", "Cache-Control" to "no-store")
private val OTHER_SECURITY_HEADERS =
    listOf("Pragma", "Expires", "X-Frame-Options", "X-XSS-Protection", "Strict-Transport-Security", "WWW-Authenticate")

/** Counts the HttpSession objects the container creates (the server must create none, Р34). */
class CreatedSessions : HttpSessionListener {
    val count = AtomicInteger()

    override fun sessionCreated(se: HttpSessionEvent) {
        count.incrementAndGet()
    }
}

@TestConfiguration(proxyBeanMethods = false)
class SecurityChainTestConfiguration {
    @Bean
    fun clock(): Clock = MovableClock(T0)

    @Bean
    fun createdSessions() = CreatedSessions()

    @Bean
    fun createdSessionsRegistration(sessions: CreatedSessions) = ServletListenerRegistrationBean(sessions)
}

/**
 * The @http scenarios of docs/specs/server/spring-security.feature against the core with the test console: what the
 * one security chain lets in, what it writes and what it leaves alone (Р34-Р37, Р47, Р48, Р51-Р53).
 */
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.RANDOM_PORT,
    properties = [
        "spring.grpc.server.port=0",
        "sard.test.admin-password=$PASSWORD",
        "sard.console.location=$FIXTURE_CONSOLE",
        // One worker thread: the next request runs on the thread of the previous one (Р48).
        "server.tomcat.threads.max=1",
    ],
)
@Import(SeededAdministrator::class, TestcontainersConfiguration::class, SecurityChainTestConfiguration::class)
class SpringSecurityHttpIntegrationTest(
    @Autowired private val mapper: ObjectMapper,
    @Autowired private val clock: Clock,
    @Autowired private val sessionStore: SessionStore,
    @Autowired private val attemptTracker: LoginAttemptTracker,
    @Autowired private val createdSessions: CreatedSessions,
    @LocalServerPort private val port: Int,
) {
    private val http = HttpClient.newHttpClient()
    private val movable get() = clock as MovableClock

    @BeforeTest
    @AfterTest
    fun `reset the clock and forget every session and lock`() {
        movable.now = T0
        sessionStore.forgetEverythingForTests()
        attemptTracker.forgetEverythingForTests()
        createdSessions.count.set(0)
    }

    private fun send(
        method: String,
        path: String,
        body: String? = null,
        cookieHeader: String? = null,
        headers: Map<String, String> = emptyMap(),
    ): HttpResponse<String> {
        val publisher = body?.let { HttpRequest.BodyPublishers.ofString(it) } ?: HttpRequest.BodyPublishers.noBody()
        val builder = HttpRequest.newBuilder(URI.create("http://localhost:$port$path")).method(method, publisher)
        if (body != null) builder.header("Content-Type", "application/json")
        cookieHeader?.let { builder.header("Cookie", it) }
        headers.forEach { (name, value) -> builder.header(name, value) }
        return http.send(builder.build(), HttpResponse.BodyHandlers.ofString())
    }

    private fun get(
        path: String,
        session: String? = null,
        headers: Map<String, String> = emptyMap(),
    ) = send("GET", path, cookieHeader = session?.let { "$SESSION_COOKIE=$it" }, headers = headers)

    private fun signIn(): String {
        val response = send("POST", "/api/v1/session", """{"password":"$PASSWORD"}""")
        return Regex("$SESSION_COOKIE=([^;]*)").find(response.headers().firstValue("Set-Cookie").orElseThrow())!!.groupValues[1]
    }

    private fun HttpResponse<String>.code() = mapper.readTree(body()).path("code").asString()

    private fun HttpResponse<String>.assertApiHeaders() {
        for ((name, value) in API_HEADERS) {
            assertEquals(listOf(value), headers().allValues(name), "$name of ${statusCode()}")
        }
    }

    private fun HttpResponse<String>.assertNoOtherSecurityHeaders() {
        for (name in OTHER_SECURITY_HEADERS) assertTrue(headers().allValues(name).isEmpty(), "$name of ${statusCode()}")
    }

    // ---- Правило: Цепочка ядра не создаёт сессий контейнера и cookie, кроме своих ----

    @Test
    fun `Мастер, вход и работа не выдают cookie, кроме sard_session и sard_setup`() {
        val session = signIn()
        val responses =
            listOf(
                get("/api/v1/onboarding"),
                get("/api/v1/sources", session),
                get("/api/v1/sources"),
                get("/api/v1/no-such-resource", session),
                send("POST", "/api/v1/sources", "{}", "$SESSION_COOKIE=$session", FOREIGN_ORIGIN),
                send("DELETE", "/api/v1/session", cookieHeader = "$SESSION_COOKIE=$session"),
                get("/"),
                get("/actuator/health"),
                get("/v3/api-docs"),
            )

        val names = responses.flatMap { it.headers().allValues("Set-Cookie") }.map { it.substringBefore('=') }
        assertTrue(names.all { it == SESSION_COOKIE || it == "sard_setup" }, "$names")
    }

    @Test
    fun `Ни один запрос не создаёт сессию контейнера сервлетов`() {
        val session = signIn()

        get("/api/v1/sources", session)
        get("/api/v1/sources")
        get("/api/v1/onboarding")
        send("DELETE", "/api/v1/session", cookieHeader = "$SESSION_COOKIE=$session")
        get("/")

        assertEquals(0, createdSessions.count.get())
    }

    @Test
    fun `Учётные данные HTTP помимо cookie сессии не открывают API`() {
        val basic = { user: String, password: String ->
            "Basic " + Base64.getEncoder().encodeToString("$user:$password".toByteArray())
        }
        val values = listOf(basic("user", PASSWORD), basic("admin", PASSWORD), basic("", ""), "Bearer forged-token")

        for (value in values) {
            val response = get("/api/v1/sources", headers = mapOf("Authorization" to value))

            assertEquals(401, response.statusCode(), value)
            assertEquals("unauthenticated", response.code())
            assertTrue(response.headers().allValues("WWW-Authenticate").isEmpty(), value)
        }
    }

    @Test
    fun `Ответ 401 без cookie не несёт заголовка WWW-Authenticate`() {
        val response = get("/api/v1/sources")

        assertEquals(401, response.statusCode())
        assertEquals("unauthenticated", response.code())
        assertEquals("application/problem+json", response.headers().firstValue("Content-Type").orElse(null))
        assertTrue(response.headers().allValues("WWW-Authenticate").isEmpty())
    }

    @Test
    fun `Пути входа и выхода Spring Security остаются путями консоли`() {
        val session = signIn()

        for (path in listOf("/login", "/logout")) {
            val page = get(path, session)
            assertEquals(200, page.statusCode(), path)
            assertTrue(page.body().contains("sard-console-fixture"), path)
        }
        for (path in listOf("/login", "/logout")) {
            val response = send("POST", path, cookieHeader = "$SESSION_COOKIE=$session")
            assertEquals(405, response.statusCode(), path)
            assertEquals(
                setOf("GET", "HEAD"),
                response
                    .headers()
                    .firstValue("Allow")
                    .orElse("")
                    .split(", ")
                    .toSet(),
                path,
            )
        }
        assertEquals(200, get("/api/v1/session", session).statusCode())
    }

    // ---- Правило: Ответы api v1 несут nosniff и no-store, прочие ответы — прежние заголовки ----

    @Test
    fun `Ответ под api v1 несёт заголовки API`() {
        val session = signIn()
        val cases =
            listOf(
                200 to get("/api/v1/status"),
                200 to get("/api/v1/session", session),
                204 to send("POST", "/api/v1/session", """{"password":"$PASSWORD"}"""),
                401 to get("/api/v1/sources"),
                401 to send("DELETE", "/api/v1/session"),
                403 to send("POST", "/api/v1/sources", "{}", "$SESSION_COOKIE=$session", FOREIGN_ORIGIN),
                404 to get("/api/v1/no-such-resource", session),
                200 to get("/api/v1/onboarding"),
            )

        for ((status, response) in cases) {
            assertEquals(status, response.statusCode(), response.uri().toString())
            response.assertApiHeaders()
            response.assertNoOtherSecurityHeaders()
        }
    }

    @Test
    fun `Ответ блокировки входа несёт заголовки API`() {
        repeat(5) { send("POST", "/api/v1/session", """{"password":"wrong-password-123"}""") }

        val locked = send("POST", "/api/v1/session", """{"password":"wrong-password-123"}""")

        assertEquals(429, locked.statusCode())
        assertEquals("too_many_attempts", locked.code())
        locked.assertApiHeaders()
    }

    @Test
    fun `Ответ с cookie сессии несёт заголовки API вместе с Set-Cookie`() {
        val response = send("POST", "/api/v1/session", """{"password":"$PASSWORD"}""")

        assertEquals(204, response.statusCode())
        assertTrue(
            response
                .headers()
                .firstValue("Set-Cookie")
                .orElse("")
                .startsWith("$SESSION_COOKIE="),
        )
        response.assertApiHeaders()
    }

    @Test
    fun `Ответ вне api v1 без своих заголовков не получает заголовков API`() {
        val outside = listOf("/actuator/health" to 200, "/v3/api-docs" to 200, "/api/v2/agents" to 404, "/api" to 404)
        for ((path, status) in outside) {
            val response = get(path)

            assertEquals(status, response.statusCode(), path)
            assertTrue(response.headers().allValues("Cache-Control").isEmpty(), path)
            assertTrue(response.headers().allValues("X-Content-Type-Options").isEmpty(), path)
        }
    }

    @Test
    fun `Ответ консоли несёт только свои заголовки и по одному`() {
        for (path in listOf("/", "/agents", "/favicon.svg")) {
            val response = get(path)

            assertEquals(200, response.statusCode(), path)
            assertEquals(listOf("no-cache"), response.headers().allValues("Cache-Control"), path)
            assertEquals(1, response.headers().allValues("X-Content-Type-Options").size, path)
            assertEquals(1, response.headers().allValues("Content-Security-Policy").size, path)
            for (name in listOf("Pragma", "Expires", "X-XSS-Protection", "Strict-Transport-Security")) {
                assertTrue(response.headers().allValues(name).isEmpty(), "$name of $path")
            }
        }
    }

    // ---- Правило: Без сессии открыто ровно то же, что раньше ----

    @Test
    fun `Публичная операция совпадает только с точным методом и путём`() {
        val requests =
            listOf(
                "GET" to "/api/v1/status/",
                "GET" to "/api/v1/STATUS",
                "HEAD" to "/api/v1/status",
                "POST" to "/api/v1/status",
                "GET" to "/api/v1/status/extra",
                "GET" to "/api/v1/onboarding/ca",
                "PUT" to "/api/v1/session",
            )

        for ((method, path) in requests) {
            val response = send(method, path)

            assertEquals(401, response.statusCode(), "$method $path")
            if (method != "HEAD") assertEquals("unauthenticated", response.code(), "$method $path")
        }
    }

    @Test
    fun `Путь вне api v1 с недействующей cookie отвечает так же, как без неё`() {
        for (path in listOf("/actuator/health", "/v3/api-docs", "/api/v2/agents", "/agents")) {
            val plain = get(path)
            val forged = send("GET", path, cookieHeader = "$SESSION_COOKIE=forged-session-id")

            assertEquals(plain.statusCode(), forged.statusCode(), path)
            assertTrue(forged.headers().allValues("Set-Cookie").isEmpty(), path)
        }
    }

    @Test
    fun `Недействующая cookie сессии под api v1 не даёт аутентификации`() {
        val valid = signIn()
        val cookies =
            listOf(
                "$SESSION_COOKIE=",
                "$SESSION_COOKIE=" + "a".repeat(4096),
                "$SESSION_COOKIE=\"quoted-forged-id\"",
                "SARD_SESSION=$valid",
            )

        for (cookie in cookies) {
            val response = send("GET", "/api/v1/sources", cookieHeader = cookie)

            assertEquals(401, response.statusCode(), cookie.take(40))
            assertEquals("unauthenticated", response.code())
        }
    }

    @Test
    fun `Из двух cookie сессии в запросе действует первая`() {
        val session = signIn()
        val valid = "$SESSION_COOKIE=$session"
        val forged = "$SESSION_COOKIE=forged-session-id"

        assertEquals(200, send("GET", "/api/v1/sources", cookieHeader = "$valid; $forged").statusCode())
        assertEquals(401, send("GET", "/api/v1/sources", cookieHeader = "$forged; $valid").statusCode())
    }

    @Test
    fun `Изменяющий запрос с чужим Origin без cookie отвечает 403, а не 401`() {
        val response = send("POST", "/api/v1/sources", "{}", headers = mapOf("Origin" to "https://evil.example"))

        assertEquals(403, response.statusCode())
        assertEquals("origin_rejected", response.code())
    }

    @Test
    fun `Изменяющий запрос на путь консоли с чужим Origin отвечает 405`() {
        val response = send("POST", "/agents", "{}", headers = mapOf("Origin" to "https://evil.example"))

        assertEquals(405, response.statusCode())
        assertEquals(
            setOf("GET", "HEAD"),
            response
                .headers()
                .firstValue("Allow")
                .orElse("")
                .split(", ")
                .toSet(),
        )
    }

    @Test
    fun `Запрос, который сессию не смотрит, её не продлевает`() {
        for ((method, path, body) in listOf(
            Triple("GET", "/api/v1/status", null),
            Triple("POST", "/api/v1/onboarding/setup-session", """{"code":"0000-0000"}"""),
        )) {
            movable.now = T0
            val session = signIn()
            movable.now = T0.plus(Duration.ofMinutes(59))

            send(method, path, body, "$SESSION_COOKIE=$session")

            movable.now = T0.plus(Duration.ofHours(1))
            assertNull(sessionStore.find(session), "$method $path")
        }
    }

    @Test
    fun `Запрос состояния онбординга с cookie сессии продлевает её`() {
        val session = signIn()
        movable.now = T0.plus(Duration.ofMinutes(59))

        get("/api/v1/onboarding", session)

        movable.now = T0.plus(Duration.ofHours(1))
        assertNotNull(sessionStore.find(session))
    }

    // ---- Правило: Контекст безопасности живёт один запрос ----

    @Test
    fun `Запрос без cookie после запроса с сессией в том же потоке отвечает 401`() {
        val session = signIn()

        assertEquals(200, get("/api/v1/sources", session).statusCode())
        val response = get("/api/v1/sources")

        assertEquals(401, response.statusCode())
        assertEquals("unauthenticated", response.code())
    }

    @Test
    fun `Одновременные запросы с сессией и без неё получают каждый свой ответ`() {
        val session = signIn()
        val pool = Executors.newFixedThreadPool(16)
        try {
            val withSession = (1..50).map { pool.submit(Callable { get("/api/v1/sources", session).statusCode() }) }
            val without = (1..50).map { pool.submit(Callable { get("/api/v1/sources").statusCode() }) }

            assertEquals(List(50) { 200 }, withSession.map { it.get() })
            assertEquals(List(50) { 401 }, without.map { it.get() })
        } finally {
            pool.shutdownNow()
        }
    }

    // ---- Правило: Документ OpenAPI ядра не меняется ----

    @Test
    fun `Документ OpenAPI ядра не получает путей и схем Spring Security`() {
        val document = mapper.readTree(get("/v3/api-docs").body())

        val paths = document.path("paths").propertyNames().toList()
        assertTrue(paths.none { it == "/login" || it == "/logout" || it.startsWith("/oauth2") }, "$paths")
        val schemes =
            document
                .path("components")
                .path("securitySchemes")
                .propertyNames()
                .toList()
                .sorted()
        assertEquals(listOf("session", "setupSession"), schemes)
    }

    // ---- Правило: Пути, которые нельзя однозначно сопоставить, отклоняются до кода сервера ----

    @Test
    fun `Ненормализованный путь отклоняется ответом 400 с пустым телом и заголовками API`() {
        val session = signIn()
        val paths =
            listOf(
                "/api;x/v1/status",
                "/api/v1;x/status",
                "/api/v1/%2e%2e/v1/status",
                "/api/v1/sources%2F..%2Fstatus",
                "/assets/%2e%2e/application.yaml",
                "/;x/actuator/health",
            )

        for (path in paths) {
            for (cookie in listOf(null, session)) {
                val response = get(path, cookie)

                assertEquals(400, response.statusCode(), path)
                assertEquals("", response.body(), "$path ${response.headers().map()}")
                assertTrue(response.headers().allValues("Content-Security-Policy").isEmpty(), path)
                response.assertApiHeaders()
            }
        }
    }
}
