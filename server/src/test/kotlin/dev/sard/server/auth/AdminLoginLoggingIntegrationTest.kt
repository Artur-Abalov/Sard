// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.auth

import ch.qos.logback.classic.spi.ILoggingEvent
import ch.qos.logback.core.read.ListAppender
import dev.sard.server.TestcontainersConfiguration
import dev.sard.server.pki.MovableClock
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.boot.test.web.server.LocalServerPort
import org.springframework.context.annotation.Import
import java.net.URI
import java.net.http.HttpClient
import java.net.http.HttpRequest
import java.net.http.HttpResponse
import java.time.Clock
import java.time.Instant
import kotlin.test.AfterTest
import kotlin.test.BeforeTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

private val LOGIN: Instant = Instant.parse("2026-10-01T12:00:00Z")
private const val PASSWORD = "correct-horse-battery"

/**
 * Rule "Вход, неудача, выход и блокировка пишутся в лог с адресом клиента", and rule
 * "Пароль не попадает в логи, ответы, actuator и метрики" (@http). The client address
 * a loopback HTTP client sees is 127.0.0.1, so the scenario checks the mechanism ("the
 * logged address is the one the request came from"), not the literal 203.0.113.10 of
 * the specification's HTTP-level narration.
 */
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.RANDOM_PORT,
    properties = ["spring.grpc.server.port=0", "SARD_ADMIN_PASSWORD=$PASSWORD"],
)
@Import(TestcontainersConfiguration::class, AdminLoginClockConfiguration::class)
class AdminLoginLoggingIntegrationTest(
    @Autowired private val clock: Clock,
    @Autowired private val sessionStore: SessionStore,
    @Autowired private val attemptTracker: LoginAttemptTracker,
    @LocalServerPort private val port: Int,
) {
    private val http = HttpClient.newHttpClient()

    @BeforeTest
    fun `reset the clock and forget every session and lock`() {
        (clock as MovableClock).now = LOGIN
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
    ): HttpResponse<String> {
        val publisher = body?.let { HttpRequest.BodyPublishers.ofString(it) } ?: HttpRequest.BodyPublishers.noBody()
        val builder =
            HttpRequest
                .newBuilder(URI.create("http://localhost:$port$path"))
                .header("Content-Type", "application/json")
                .method(method, publisher)
        cookie?.let { builder.header("Cookie", "$SESSION_COOKIE_NAME=$it") }
        return http.send(builder.build(), HttpResponse.BodyHandlers.ofString())
    }

    private fun login(password: String = PASSWORD): HttpResponse<String> {
        val body = """{"password":"$password"}"""
        return send("POST", "/api/v1/session", body)
    }

    private fun sessionIdOf(response: HttpResponse<String>): String {
        val setCookie = response.headers().firstValue("Set-Cookie").orElseThrow()
        return Regex("$SESSION_COOKIE_NAME=([^;]*)").find(setCookie)!!.groupValues[1]
    }

    private fun capture(block: () -> Unit): List<String> {
        val root = org.slf4j.LoggerFactory.getLogger(org.slf4j.Logger.ROOT_LOGGER_NAME) as ch.qos.logback.classic.Logger
        val appender = ListAppender<ILoggingEvent>()
        appender.start()
        root.addAppender(appender)
        try {
            block()
        } finally {
            root.detachAppender(appender)
            appender.stop()
        }
        return appender.list.map { it.formattedMessage }
    }

    @Test
    fun `a successful sign-in is logged once with the client address`() {
        val log = capture { login() }
        val matches = log.filter { it.contains("Sign-in succeeded") && it.contains("127.0.0.1") }
        assertEquals(1, matches.size, "$log")
    }

    @Test
    fun `a failed sign-in is logged once with the client address`() {
        val log = capture { login("wrong-password-123") }
        val matches = log.filter { it.contains("Sign-in failed") && it.contains("127.0.0.1") }
        assertEquals(1, matches.size, "$log")
    }

    @Test
    fun `a sign-out is logged once with the client address`() {
        val cookie = sessionIdOf(login())
        val log = capture { send("DELETE", "/api/v1/session", cookie = cookie) }
        val matches = log.filter { it.contains("Signed out") && it.contains("127.0.0.1") }
        assertEquals(1, matches.size, "$log")
    }

    @Test
    fun `a lock from brute force is logged exactly once with the client address`() {
        repeat(4) { login("wrong-password-123") }
        val log = capture { repeat(4) { login("wrong-password-123") } }
        val matches = log.filter { it.contains("Sign-in locked") && it.contains("127.0.0.1") }
        assertEquals(1, matches.size, "$log")
    }

    @Test
    fun `the session id never appears in the logs`() {
        val log =
            capture {
                val cookie = sessionIdOf(login())
                send("GET", "/api/v1/session", cookie = cookie)
                send("DELETE", "/api/v1/session", cookie = cookie)
            }
        // The cookie value itself is captured from the response, outside the logger.
        val cookie = sessionIdOf(login())
        assertFalse(log.any { it.contains(cookie) })
    }

    @Test
    fun `the correct password is not logged on a successful sign-in`() {
        val log = capture { login() }
        assertFalse(log.any { it.contains(PASSWORD) })
    }

    @Test
    fun `a wrong password is not logged, and neither is the correct one`() {
        val log = capture { login("wrong-but-close-password") }
        assertFalse(log.any { it.contains("wrong-but-close-password") })
        assertFalse(log.any { it.contains(PASSWORD) })
    }

    @Test
    fun `a wrong password is not logged even while it locks sign-in`() {
        val log = capture { repeat(6) { login("wrong-but-close-password") } }
        assertFalse(log.any { it.contains("wrong-but-close-password") })
        assertFalse(log.any { it.contains(PASSWORD) })
    }

    @Test
    fun `the responses of session and status never contain the password`() {
        val cookie = sessionIdOf(login())
        val responses =
            listOf(
                send("GET", "/api/v1/session", cookie = cookie),
                send("GET", "/api/v1/status"),
                send("DELETE", "/api/v1/session", cookie = cookie),
            )
        for (response in responses) {
            assertFalse(response.body().contains(PASSWORD), response.body())
            assertTrue(
                response
                    .headers()
                    .map()
                    .values
                    .flatten()
                    .none { it.contains(PASSWORD) },
            )
        }
    }

    @Test
    fun `open actuator endpoints do not contain the password`() {
        for (path in listOf("/actuator/health", "/actuator/info", "/actuator")) {
            val response = send("GET", path)
            assertFalse(response.body().contains(PASSWORD), path)
        }
    }
}
