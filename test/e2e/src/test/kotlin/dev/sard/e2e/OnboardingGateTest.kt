// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.e2e

import org.junit.jupiter.api.extension.RegisterExtension
import java.net.CookieManager
import java.net.URI
import java.net.http.HttpClient
import java.net.http.HttpRequest
import java.net.http.HttpResponse
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

/**
 * docs/specs/server/onboarding-self-backup.feature, @e2e "Чистый compose до подтверждения не выпускает токены
 * обычных агентов": after the wizard of the first start and before the self-backup and the key copy the server
 * refuses to issue an enrollment token, 409 onboarding_incomplete, and still answers reads. The steps of the
 * self-backup in S3 are not run here: they need the S3 stand and are checked by hand (docs/qa/onboarding-self-backup.md).
 */
class OnboardingGateTest {
    private val http = HttpClient.newBuilder().cookieHandler(CookieManager()).build()

    private fun send(
        method: String,
        path: String,
        body: String?,
    ): HttpResponse<String> {
        val request =
            HttpRequest
                .newBuilder(URI.create(sard.httpBase + path))
                .header("Content-Type", "application/json")
                .method(method, body?.let(HttpRequest.BodyPublishers::ofString) ?: HttpRequest.BodyPublishers.noBody())
                .build()
        return http.send(request, HttpResponse.BodyHandlers.ofString())
    }

    @Test
    fun `after the wizard the server issues no enrollment token until the onboarding is complete`() {
        SetupWizard(sard).complete("e2e-admin-password")
        assertEquals(204, send("POST", "/api/v1/session", """{"password":"e2e-admin-password"}""").statusCode())

        val refused = send("POST", "/api/v1/enrollment-tokens", "{}")

        assertEquals(409, refused.statusCode(), refused.body())
        assertTrue("\"code\":\"onboarding_incomplete\"" in refused.body(), refused.body())
        assertEquals(200, send("GET", "/api/v1/enrollment-tokens", null).statusCode())
        sard.confirmOnboarding()
        assertEquals(201, send("POST", "/api/v1/enrollment-tokens", "{}").statusCode())
    }

    companion object {
        @JvmField
        @RegisterExtension
        val sard = SardEnvironment()
    }
}
