// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.e2e

import org.junit.jupiter.api.MethodOrderer
import org.junit.jupiter.api.Order
import org.junit.jupiter.api.TestMethodOrder
import org.junit.jupiter.api.extension.RegisterExtension
import java.net.URI
import java.net.http.HttpClient
import java.net.http.HttpRequest
import java.net.http.HttpResponse
import kotlin.test.Test
import kotlin.test.assertEquals

/**
 * The first start of a clean installation, as the owner does it (docs/specs/server/onboarding-setup.feature,
 * @e2e): the server prints one setup code, refuses to sign anyone in, and after the wizard — code, CA,
 * password — signs in with that password and with no other. The tests run in order on one installation.
 */
@TestMethodOrder(MethodOrderer.OrderAnnotation::class)
class FirstStartTest {
    private fun signIn(password: String): Int {
        val request =
            HttpRequest
                .newBuilder(URI.create("${sard.httpBase}/api/v1/session"))
                .header("Content-Type", "application/json")
                .POST(HttpRequest.BodyPublishers.ofString("""{"password":"$password"}"""))
                .build()
        return HttpClient.newHttpClient().send(request, HttpResponse.BodyHandlers.discarding()).statusCode()
    }

    @Test
    @Order(1)
    fun `a clean installation prints one setup code and signs nobody in`() {
        assertEquals(1, SetupCode.count(sard.serverLogs()), "code lines in the log")
        assertEquals(409, signIn("correct-horse-battery"))
    }

    @Test
    @Order(2)
    fun `the wizard by the code from the log sets the password that signs in`() {
        SetupWizard(sard).complete("e2e-admin-password")
        assertEquals(204, signIn("e2e-admin-password"))
        assertEquals(401, signIn("e2e-another-password"))
    }

    @Test
    @Order(3)
    fun `a restart prints no new code and keeps the password`() {
        sard.restartServer()
        assertEquals(1, SetupCode.count(sard.serverLogs()), "code lines in the log after the restart")
        assertEquals(204, signIn("e2e-admin-password"))
    }

    companion object {
        @JvmField
        @RegisterExtension
        val sard = SardEnvironment()
    }
}
