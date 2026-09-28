// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.auth

import dev.sard.server.TestcontainersConfiguration
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.boot.test.web.server.LocalServerPort
import org.springframework.context.annotation.Import
import java.net.URI
import java.net.http.HttpClient
import java.net.http.HttpRequest
import java.net.http.HttpResponse
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

private const val PASSWORD = "пароль-из-кириллицы-42"

/** Rule "Верный пароль выдаёт сессию", non-ASCII case: a password outside ASCII is accepted. */
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.RANDOM_PORT,
    properties = ["spring.grpc.server.port=0", "SARD_ADMIN_PASSWORD=$PASSWORD"],
)
@Import(TestcontainersConfiguration::class)
class NonAsciiPasswordIntegrationTest(
    @LocalServerPort private val port: Int,
) {
    @Test
    fun `a password with non-ASCII characters is accepted`() {
        val http = HttpClient.newHttpClient()
        val request =
            HttpRequest
                .newBuilder(URI.create("http://localhost:$port/api/v1/session"))
                .header("Content-Type", "application/json")
                .POST(HttpRequest.BodyPublishers.ofString("""{"password":"$PASSWORD"}"""))
                .build()
        val response = http.send(request, HttpResponse.BodyHandlers.ofString())
        assertEquals(204, response.statusCode())
        assertTrue(
            response
                .headers()
                .firstValue("Set-Cookie")
                .orElse("")
                .contains(SESSION_COOKIE_NAME),
        )
    }
}
