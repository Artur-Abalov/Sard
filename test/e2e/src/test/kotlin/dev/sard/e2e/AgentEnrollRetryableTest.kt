// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.e2e

import org.junit.jupiter.api.extension.RegisterExtension
import java.net.URI
import java.net.http.HttpClient
import java.net.http.HttpRequest
import java.net.http.HttpResponse
import java.time.Duration
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

/**
 * The `@e2e` scenario of docs/specs/agent/agent-enroll.feature that takes the server's database
 * away, alone in its installation so no other test sees it down. PostgreSQL is stopped and
 * started again with `docker stop`/`docker start`: the container, its data and the token stay.
 */
class AgentEnrollRetryableTest {
    @Test
    fun `После INTERNAL_RETRYABLE тем же токеном можно зарегистрироваться`() {
        val token = EnrollmentTokens.create(sard)
        val host = AgentHost(sard)
        val docker = sard.postgres.dockerClient
        docker.stopContainerCmd(sard.postgres.containerId).exec()

        // Longer than the server's wait for a database connection, so the server answers.
        val refused = host.enroll(token, "--timeout", "90s")
        assertEquals(TEMPORARY, refused.code, refused.stderr)
        assertTrue("INTERNAL_RETRYABLE" in refused.stderr, "the message does not name INTERNAL_RETRYABLE: ${refused.stderr}")

        docker.startContainerCmd(sard.postgres.containerId).exec()
        Await.until("the server's health with its database back", Duration.ofMinutes(2)) { healthy() }
        val exit = host.enroll(token)

        assertEquals(SUCCESS, exit.code, exit.stderr)
    }

    /** The server's health check, which includes its database connection (Spring Boot's db indicator). */
    private fun healthy(): Boolean {
        val request = HttpRequest.newBuilder(URI.create(sard.httpBase + "/actuator/health")).build()
        return runCatching { HttpClient.newHttpClient().send(request, HttpResponse.BodyHandlers.discarding()).statusCode() == 200 }
            .getOrDefault(false)
    }

    companion object {
        @JvmField
        @RegisterExtension
        val sard = SardEnvironment()

        private const val SUCCESS = 0
        private const val TEMPORARY = 6
    }
}
