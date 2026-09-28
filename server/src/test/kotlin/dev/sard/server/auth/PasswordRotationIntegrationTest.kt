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

private const val OLD_PASSWORD = "correct-horse-battery"
private const val NEW_PASSWORD = "new-password-2026"

/**
 * Rule "Пароль меняется только переменной окружения и перезапуском". A distinct
 * SARD_ADMIN_PASSWORD gives this class its own Spring context (a fresh process, in
 * effect): the equivalent of restarting the server with a new password.
 */
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.RANDOM_PORT,
    properties = ["spring.grpc.server.port=0", "SARD_ADMIN_PASSWORD=$NEW_PASSWORD"],
)
@Import(TestcontainersConfiguration::class)
class PasswordRotationIntegrationTest(
    @LocalServerPort private val port: Int,
) {
    private fun login(password: String): HttpResponse<String> {
        val request =
            HttpRequest
                .newBuilder(URI.create("http://localhost:$port/api/v1/session"))
                .header("Content-Type", "application/json")
                .POST(HttpRequest.BodyPublishers.ofString("""{"password":"$password"}"""))
                .build()
        return HttpClient.newHttpClient().send(request, HttpResponse.BodyHandlers.ofString())
    }

    @Test
    fun `after restarting with a new password the old one is refused`() {
        assertEquals(401, login(OLD_PASSWORD).statusCode())
    }

    @Test
    fun `after restarting with a new password it is accepted`() {
        assertEquals(204, login(NEW_PASSWORD).statusCode())
    }
}
