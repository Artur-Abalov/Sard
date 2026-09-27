// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.enrollment

import dev.sard.server.persistence.EnrollmentTokenRecord
import dev.sard.server.persistence.TenantSessions
import dev.sard.server.persistence.UuidV7
import dev.sard.server.pki.CertificateAuthority
import java.security.SecureRandom
import java.time.Clock
import java.time.Duration
import java.time.Instant
import java.util.UUID

private const val BY_HASH = "from EnrollmentTokenRecord where tokenHash = :hash"

/** A freshly created token: the only moment its string exists on the server. */
class IssuedEnrollmentToken(
    val id: UUID,
    val expiresAt: Instant,
    private val token: String,
) {
    /** The string to show the operator once; nothing keeps it after the response. */
    fun reveal(): String = token

    override fun toString() = "IssuedEnrollmentToken(id=$id, expiresAt=$expiresAt)"
}

/** Which token a secret belongs to, and so which tenant the enrolling agent joins. */
data class TokenOwner(
    val tokenId: UUID,
    val tenantId: UUID,
)

/** Creates enrollment tokens and finds their owner by hash (docs/specs/enrollment-token.md). */
class EnrollmentTokens(
    private val sessions: TenantSessions,
    private val ca: CertificateAuthority,
    private val clock: Clock,
    private val random: SecureRandom,
    private val ids: UuidV7,
) {
    /** A token for [tenantId] valid for [ttl]; only the hash of its secret is stored. */
    fun create(
        tenantId: UUID,
        ttl: Duration,
    ): IssuedEnrollmentToken {
        require(ttl.isPositive) { "an enrollment token must live for a positive duration" }
        val secret = EnrollmentSecret.random(random)
        val now = clock.instant()
        val record = EnrollmentTokenRecord(ids.next(), secret.hash(), expiresAt = now + ttl, createdAt = now)
        sessions.inTenant(tenantId) { it.persist(record) }
        return IssuedEnrollmentToken(record.id, record.expiresAt, EnrollmentToken(secret, ca.fingerprint()).encode())
    }

    /** The one system lookup of enrollment: by hash, across tenants, before the tenant is known. */
    fun ownerOf(tokenHash: ByteArray): TokenOwner? =
        sessions.system { session ->
            session
                .createSelectionQuery(BY_HASH, EnrollmentTokenRecord::class.java)
                .setParameter("hash", tokenHash)
                .uniqueResult()
                ?.let { TokenOwner(it.id, checkNotNull(it.tenantId)) }
        }
}
