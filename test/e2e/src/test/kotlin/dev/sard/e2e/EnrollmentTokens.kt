// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.e2e

import java.security.MessageDigest
import java.security.SecureRandom
import java.sql.Timestamp
import java.time.Duration
import java.time.Instant
import java.util.Base64
import java.util.UUID

/**
 * Creates enrollment tokens the way an operator would get them.
 *
 * REPLACEMENT POINT (S8b): [create] is the only place tests get a token. While
 * `POST /api/v1/enrollment-tokens` answers 501, it writes the row the server's
 * token service writes, strictly by docs/specs/enrollment-token.md. Once S8b is
 * in main, replace its body with that POST (`{"ttlSeconds": ...}` → `token`)
 * and delete [insert]; [format] and [hash] then only serve the vector test.
 */
internal object EnrollmentTokens {
    /** The open core's only tenant (`TenantResolver.DEFAULT_TENANT_ID`, V2__tenants.sql). */
    private val DEFAULT_TENANT: UUID = UUID.fromString("00000000-0000-0000-0000-000000000001")
    private const val SECRET_BYTES = 32
    private val random = SecureRandom()

    /**
     * A fresh single-use token for [env]'s server, valid for [ttl]. The
     * fingerprint is the root of the chain the gRPC port presents, as an agent
     * would pin it. The token is registered as a secret of [env]'s logs.
     */
    fun create(
        env: SardEnvironment,
        ttl: Duration = Duration.ofHours(1),
        label: String = "e2e",
    ): String {
        val secret = ByteArray(SECRET_BYTES).also(random::nextBytes)
        val fingerprint = ServerTls.fingerprint(ServerTls.presentedChain(env).last())
        insert(env, hash(secret), ttl, label)
        return format(secret, fingerprint).also(env::secret)
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

    private fun insert(
        env: SardEnvironment,
        tokenHash: ByteArray,
        ttl: Duration,
        label: String,
    ) {
        val now = Instant.now()
        env.database().use { connection ->
            connection
                .prepareStatement(
                    """
                    INSERT INTO enrollment_tokens (id, tenant_id, token_hash, expires_at, created_at, label)
                    VALUES (?, ?, ?, ?, ?, ?)
                    """.trimIndent(),
                ).use { insert ->
                    insert.setObject(1, UUID.randomUUID())
                    insert.setObject(2, DEFAULT_TENANT)
                    insert.setBytes(3, tokenHash)
                    insert.setTimestamp(4, Timestamp.from(now + ttl))
                    insert.setTimestamp(5, Timestamp.from(now))
                    insert.setString(6, label)
                    insert.executeUpdate()
                }
        }
    }
}
