// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.e2e

import java.net.CookieManager
import java.net.URI
import java.net.http.HttpClient
import java.net.http.HttpRequest
import java.net.http.HttpResponse
import java.security.MessageDigest
import java.time.Duration
import java.util.Base64
import java.util.UUID

/**
 * Creates enrollment tokens the way an operator does: signs in to the server's REST API as the
 * administrator and asks `POST /api/v1/enrollment-tokens` for one (S8b). Nothing here writes to the
 * database; [format] and [hash] only serve the vector test of docs/specs/enrollment-token.md.
 */
internal object EnrollmentTokens {
    /** The open core's only tenant (`TenantResolver.DEFAULT_TENANT_ID`, V2__tenants.sql). */
    val DEFAULT_TENANT: UUID = UUID.fromString("00000000-0000-0000-0000-000000000001")
    private const val SECRET_BYTES = 32
    private val TOKEN = Regex("\"token\"\\s*:\\s*\"(sard_[A-Za-z0-9_-]{43}\\.[0-9a-f]{64})\"")

    /**
     * A fresh single-use token for [env]'s server, valid for [ttl], from the REST API. The token is
     * registered as a secret of [env]'s logs.
     */
    fun create(
        env: SardEnvironment,
        ttl: Duration = Duration.ofHours(1),
        label: String = "e2e",
    ): String {
        val http = HttpClient.newBuilder().cookieHandler(CookieManager()).build()
        val signIn = post(http, env, "/api/v1/session", """{"password":"${env.adminPassword}"}""")
        check(signIn.statusCode() == HTTP_NO_CONTENT) { "sign-in answered ${signIn.statusCode()}" }
        val created = post(http, env, "/api/v1/enrollment-tokens", """{"ttlSeconds":${ttl.seconds},"label":"$label"}""")
        check(created.statusCode() == HTTP_CREATED) { "creating a token answered ${created.statusCode()}" }
        val token = checkNotNull(TOKEN.find(created.body())) { "the answer has no token" }.groupValues[1]
        return token.also(env::secret)
    }

    private fun post(
        http: HttpClient,
        env: SardEnvironment,
        path: String,
        body: String,
    ): HttpResponse<String> =
        http.send(
            HttpRequest
                .newBuilder(URI.create(env.httpBase + path))
                .header("Content-Type", "application/json")
                .POST(HttpRequest.BodyPublishers.ofString(body))
                .build(),
            HttpResponse.BodyHandlers.ofString(),
        )

    /** `sard_<base64url secret, no padding>.<fingerprint>` (spec: "Формат"). */
    fun format(
        secret: ByteArray,
        fingerprint: String,
    ): String {
        require(secret.size == SECRET_BYTES) { "secret must be $SECRET_BYTES bytes" }
        return "sard_" + Base64.getUrlEncoder().withoutPadding().encodeToString(secret) + "." + fingerprint
    }

    /** `token_hash`: SHA-256 of the 32 raw secret bytes, not of the string (spec: "Хранение"). */
    fun hash(secret: ByteArray): ByteArray = MessageDigest.getInstance("SHA-256").digest(secret)

    private const val HTTP_NO_CONTENT = 204
    private const val HTTP_CREATED = 201
}
