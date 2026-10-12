// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.auth

import dev.sard.server.SeededAdministrator
import dev.sard.server.TestcontainersConfiguration
import dev.sard.server.api.SESSION_COOKIE
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.boot.test.web.server.LocalServerPort
import org.springframework.context.annotation.Import
import org.springframework.test.annotation.DirtiesContext
import org.testcontainers.postgresql.PostgreSQLContainer
import java.net.URI
import java.net.http.HttpClient
import java.net.http.HttpRequest
import java.net.http.HttpResponse
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

// A password unique to this class so it gets its own Spring context and its own
// Testcontainers Postgres (stopped below), never shared with another test's cache entry.
private const val PASSWORD = "db-unavailable-password"

/**
 * Rule "Вход до создания администратора отвечает 409 setup_required", scenarios "Вход при недоступной базе
 * отвечает 503 и не засчитывается" and "Текущая сессия читается при недоступной базе" (OQ-188): the hash of
 * the password is in the database, so sign-in needs it; the sessions are in memory, so reading one does not.
 * [DirtiesContext] retires this class's context (and its now-stopped container) instead of returning it to the
 * cache for reuse.
 */
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.RANDOM_PORT,
    properties = ["spring.grpc.server.port=0", "sard.test.admin-password=$PASSWORD"],
)
@Import(SeededAdministrator::class, TestcontainersConfiguration::class)
@DirtiesContext
class DatabaseUnavailableIntegrationTest(
    @Autowired private val postgres: PostgreSQLContainer,
    @LocalServerPort private val port: Int,
) {
    private val http = HttpClient.newHttpClient()

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
        cookie?.let { builder.header("Cookie", "$SESSION_COOKIE=$it") }
        return http.send(builder.build(), HttpResponse.BodyHandlers.ofString())
    }

    private fun sessionIdOf(response: HttpResponse<String>): String =
        Regex(
            "$SESSION_COOKIE=([^;]*)",
        ).find(response.headers().firstValue("Set-Cookie").orElseThrow())!!.groupValues[1]

    private fun login(password: String) = send("POST", "/api/v1/session", """{"password":"$password"}""")

    @Test
    fun `sign-in answers 503 without counting and the current session is read while the database is down`() {
        val cookie = sessionIdOf(login(PASSWORD))
        repeat(4) { assertEquals(401, login("wrong-password-123").statusCode()) }

        postgres.stop()

        repeat(3) {
            val response = login("wrong-password-123")
            assertEquals(503, response.statusCode())
            assertEquals("application/problem+json", response.headers().firstValue("Content-Type").orElse(""))
            assertTrue("\"unavailable\"" in response.body(), response.body())
        }
        assertEquals(200, send("GET", "/api/v1/session", cookie = cookie).statusCode())
    }
}
