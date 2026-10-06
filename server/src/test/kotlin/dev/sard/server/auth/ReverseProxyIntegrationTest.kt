// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.auth

import ch.qos.logback.classic.spi.ILoggingEvent
import ch.qos.logback.core.read.ListAppender
import dev.sard.server.TestcontainersConfiguration
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.boot.test.web.server.LocalServerPort
import org.springframework.context.annotation.Import
import java.net.URI
import java.net.http.HttpClient
import java.net.http.HttpRequest
import java.net.http.HttpResponse
import kotlin.test.AfterTest
import kotlin.test.BeforeTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

private const val PASSWORD = "correct-horse-battery"
private const val PUBLIC_HOST = "console.example.com"
private const val PUBLIC_ORIGIN = "https://$PUBLIC_HOST"
private const val CLIENT = "203.0.113.10"
private const val OTHER_CLIENT = "198.51.100.7"

/** A request as a TLS-terminating proxy (cloudflared, nginx, Caddy) passes it on over plain HTTP. */
private class ProxiedClient(
    private val port: Int,
) {
    private val http = HttpClient.newHttpClient()

    fun login(
        password: String = PASSWORD,
        origin: String = PUBLIC_ORIGIN,
        forwardedFor: String = CLIENT,
    ): HttpResponse<String> {
        val request =
            HttpRequest
                .newBuilder(URI.create("http://localhost:$port/api/v1/session"))
                .header("Content-Type", "application/json")
                .header("Host", PUBLIC_HOST)
                .header("Origin", origin)
                .header("X-Forwarded-Proto", "https")
                .header("X-Forwarded-For", forwardedFor)
                .POST(HttpRequest.BodyPublishers.ofString("""{"password":"$password"}"""))
                .build()
        return http.send(request, HttpResponse.BodyHandlers.ofString())
    }
}

private fun setCookieOf(response: HttpResponse<String>): String {
    val headers = response.headers()
    return headers.firstValue("Set-Cookie").orElseThrow()
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

/**
 * ADR 0045: with SARD_FORWARD_HEADERS=native the server takes the scheme and the client
 * address from X-Forwarded-Proto and X-Forwarded-For of a proxy on a trusted address (the
 * test client connects from 127.0.0.1, which Tomcat trusts by default).
 */
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.RANDOM_PORT,
    properties = ["spring.grpc.server.port=0", "SARD_ADMIN_PASSWORD=$PASSWORD", "SARD_FORWARD_HEADERS=native"],
)
@Import(TestcontainersConfiguration::class)
class ReverseProxyIntegrationTest(
    @Autowired private val sessionStore: SessionStore,
    @Autowired private val attemptTracker: LoginAttemptTracker,
    @LocalServerPort port: Int,
) {
    private val client = ProxiedClient(port)

    @BeforeTest
    @AfterTest
    fun `forget every session and lock`() {
        sessionStore.forgetEverythingForTests()
        attemptTracker.forgetEverythingForTests()
    }

    @Test
    fun `sign-in behind a TLS-terminating proxy succeeds with a Secure cookie`() {
        val response = client.login()
        assertEquals(204, response.statusCode(), response.body())
        assertTrue(setCookieOf(response).contains("Secure"), setCookieOf(response))
    }

    @Test
    fun `a foreign Origin is still rejected behind the proxy`() {
        val response = client.login(origin = "https://evil.example.com")
        assertEquals(403, response.statusCode())
        assertTrue(response.body().contains("origin_rejected"), response.body())
    }

    @Test
    fun `the plain-http Origin no longer matches once the proxy says https`() {
        assertEquals(403, client.login(origin = "http://$PUBLIC_HOST").statusCode())
    }

    @Test
    fun `the client address is taken from X-Forwarded-For`() {
        val log = capture { client.login() }
        assertTrue(log.any { it.contains("Sign-in succeeded") && it.contains(CLIENT) }, "$log")
    }

    @Test
    fun `brute force locks the forwarded client address, not the proxy`() {
        repeat(5) { client.login("wrong-password-123") }
        assertEquals(429, client.login().statusCode())
        assertEquals(204, client.login(forwardedFor = OTHER_CLIENT).statusCode())
    }
}

/** ADR 0045: without SARD_FORWARD_HEADERS proxy headers are ignored, as in ADR 0021 (Р4). */
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.RANDOM_PORT,
    properties = ["spring.grpc.server.port=0", "SARD_ADMIN_PASSWORD=$PASSWORD"],
)
@Import(TestcontainersConfiguration::class)
class ReverseProxyHeadersIgnoredByDefaultIntegrationTest(
    @Autowired private val sessionStore: SessionStore,
    @Autowired private val attemptTracker: LoginAttemptTracker,
    @LocalServerPort port: Int,
) {
    private val client = ProxiedClient(port)

    @BeforeTest
    @AfterTest
    fun `forget every session and lock`() {
        sessionStore.forgetEverythingForTests()
        attemptTracker.forgetEverythingForTests()
    }

    @Test
    fun `the https Origin from a proxy is rejected by default`() {
        val response = client.login()
        assertEquals(403, response.statusCode())
        assertTrue(response.body().contains("origin_rejected"), response.body())
    }

    @Test
    fun `X-Forwarded-For is not trusted by default`() {
        val log = capture { client.login(origin = "http://$PUBLIC_HOST") }
        assertTrue(log.any { it.contains("Sign-in succeeded") && it.contains("127.0.0.1") }, "$log")
        assertFalse(log.any { it.contains(CLIENT) }, "$log")
    }

    @Test
    fun `the cookie is not Secure by default even if the proxy says https`() {
        val response = client.login(origin = "http://$PUBLIC_HOST")
        assertEquals(204, response.statusCode(), response.body())
        assertFalse(setCookieOf(response).contains("Secure"), setCookieOf(response))
    }
}
