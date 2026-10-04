// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.e2e

import java.security.MessageDigest
import java.time.Duration
import java.util.Base64
import java.util.UUID

/**
 * Creates enrollment tokens the way an operator does: signs in to the server's REST API as the
 * administrator and asks `POST /api/v1/enrollment-tokens` for one (S8b), reads their state and
 * revokes them there. Only [expire] writes to the database: no operator can backdate a token.
 * [format] and [hash] serve the vector test of docs/specs/enrollment-token.md.
 */
internal object EnrollmentTokens {
    /** The open core's only tenant (`TenantResolver.DEFAULT_TENANT_ID`, V2__tenants.sql). */
    val DEFAULT_TENANT: UUID = UUID.fromString("00000000-0000-0000-0000-000000000001")
    private const val SECRET_BYTES = 32
    private val TOKEN = Regex("sard_[A-Za-z0-9_-]{43}\\.[0-9a-f]{64}")

    /** A token and its id in the REST API. */
    class Issued(
        val id: UUID,
        val token: String,
    ) {
        override fun toString() = "Issued(id=$id)"
    }

    /**
     * A fresh single-use token for [env]'s server, valid for [ttl], from the REST API. The token is
     * registered as a secret of [env]'s logs.
     */
    fun create(
        env: SardEnvironment,
        ttl: Duration = Duration.ofHours(1),
        label: String = "e2e",
    ): String = issue(env, ttl, label).token

    /** As [create], with the token's id. */
    fun issue(
        env: SardEnvironment,
        ttl: Duration = Duration.ofHours(1),
        label: String = "e2e",
    ): Issued {
        val created = SardApi(env).created("/api/v1/enrollment-tokens", """{"ttlSeconds":${ttl.seconds},"label":"$label"}""")
        val token = created.require("token")
        check(TOKEN.matches(token)) { "the answer has no well-formed token" }
        env.secret(token)
        return Issued(UUID.fromString(created.require("id")), token)
    }

    /** The token's state as the console shows it: `active`, `used`, `expired` or `revoked`. */
    fun status(
        env: SardEnvironment,
        id: UUID,
    ): String = SardApi(env).get("/api/v1/enrollment-tokens/$id").require("status")

    /** The agent a used token names, or null. */
    fun agentOf(
        env: SardEnvironment,
        id: UUID,
    ): String? = SardApi(env).get("/api/v1/enrollment-tokens/$id").field("agentId")

    /** Revokes the token as the console's button does. */
    fun revoke(
        env: SardEnvironment,
        id: UUID,
    ) {
        SardApi(env).created("/api/v1/enrollment-tokens/$id/revoke", expected = SardApi.HTTP_OK)
    }

    /** Moves the token's life into the past: created two hours ago, expired an hour ago. */
    fun expire(
        env: SardEnvironment,
        id: UUID,
    ) {
        env.database().use { connection ->
            val sql = "UPDATE enrollment_tokens SET created_at = now() - interval '2 hours', expires_at = now() - interval '1 hour' WHERE id = ?"
            connection.prepareStatement(sql).use { update ->
                update.setObject(1, id)
                check(update.executeUpdate() == 1) { "no token $id" }
            }
        }
    }

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
}
