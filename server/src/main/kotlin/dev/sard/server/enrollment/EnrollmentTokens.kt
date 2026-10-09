// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.enrollment

import dev.sard.server.persistence.EnrollmentTokenRecord
import dev.sard.server.persistence.PageKey
import dev.sard.server.persistence.TenantSessions
import dev.sard.server.persistence.UuidV7
import dev.sard.server.persistence.hqlWhere
import dev.sard.server.pki.CertificateAuthority
import org.hibernate.Session
import java.security.SecureRandom
import java.time.Clock
import java.time.Duration
import java.time.Instant
import java.util.UUID

private const val BY_HASH = "from EnrollmentTokenRecord where tokenHash = :hash"
private const val REVOKE =
    "update EnrollmentTokenRecord set revokedAt = :now where id = :id and builtin = false " +
        "and usedAt is null and revokedAt is null and expiresAt > :now"
private const val REVOKE_ACTIVE_BUILTIN =
    "update EnrollmentTokenRecord set revokedAt = :now where builtin = true " +
        "and usedAt is null and revokedAt is null and expiresAt > :now"
private const val USABLE_BUILTIN =
    "select count(t) from EnrollmentTokenRecord t where t.builtin = true and t.tokenHash = :hash " +
        "and t.usedAt is null and t.revokedAt is null and t.expiresAt > :limit"
private const val LABEL_MAX_LENGTH = 200

/** The lifetime of a token created without one (decision 1). */
val DEFAULT_TTL: Duration = Duration.ofHours(24)
private val MIN_TTL: Duration = Duration.ofMinutes(5)
private val MAX_TTL: Duration = Duration.ofDays(7)

/** The lifetime of a built-in token (docs/specs/server/self-agent.feature, Р1). */
val BUILTIN_TTL: Duration = Duration.ofHours(1)

/** A freshly created token: the only moment its string exists on the server. */
class IssuedEnrollmentToken(
    val id: UUID,
    val expiresAt: Instant,
    private val token: String,
    private val endpoint: AgentEndpoint,
) {
    /** The string to show the operator once; nothing keeps it after the response. */
    fun reveal(): String = token

    /** The ready-made `sard-agent enroll` invocation (decision 5); the console never builds it itself. */
    fun command(): String = endpoint.enrollCommand(token)

    /** Whether the address in [command] was named by the operator (SARD_AGENT_ENDPOINT) and not guessed (S8b В3). */
    fun endpointConfigured(): Boolean = endpoint.explicit

    override fun toString() = "IssuedEnrollmentToken(id=$id, expiresAt=$expiresAt)"
}

/** Which token a secret belongs to, and so which tenant the enrolling agent joins. */
data class TokenOwner(
    val tokenId: UUID,
    val tenantId: UUID,
    /** Whether the token was written by the server for the agent next to it; its agent is built in too. */
    val builtin: Boolean = false,
)

/** A token as an administrator sees it: never the string, never the secret (rule "Строка токена..."). */
data class EnrollmentTokenSummary(
    val id: UUID,
    val state: EnrollmentTokenState,
    val createdAt: Instant,
    val expiresAt: Instant,
    val label: String,
    val usedAt: Instant?,
    val agentId: UUID?,
    val revokedAt: Instant?,
)

/** [tokens.create] rejected the request; [field] names what was out of range. */
class EnrollmentTokenValidationException(
    val field: String,
    message: String,
) : IllegalArgumentException(message)

/** Why [EnrollmentTokens.revoke] refused (decision 6, S8b В1: "использован" or "истёк"). */
enum class RevokeRejection { USED, EXPIRED }

/** The outcome of [EnrollmentTokens.revoke]. */
sealed interface RevokeResult {
    /** Revoked now, or earlier: revoking a revoked token succeeds and keeps its [revokedAt]. */
    data class Revoked(
        val revokedAt: Instant,
    ) : RevokeResult

    data class Rejected(
        val reason: RevokeRejection,
    ) : RevokeResult

    data object NotFound : RevokeResult
}

/** Creates, lists, revokes and finds by hash enrollment tokens (docs/specs/enrollment-token.md). */
class EnrollmentTokens(
    private val sessions: TenantSessions,
    private val ca: CertificateAuthority,
    private val clock: Clock,
    private val random: SecureRandom,
    private val ids: UuidV7,
    private val endpoint: AgentEndpoint,
) {
    /**
     * A token for [tenantId], valid for [ttl] (default 24h, 5 minutes to 7 days inclusive,
     * decision 1) with an optional operator-facing [label] up to 200 characters (decision 2).
     * Only the hash of its secret is stored.
     */
    fun create(
        tenantId: UUID,
        ttl: Duration = DEFAULT_TTL,
        label: String = "",
    ): IssuedEnrollmentToken {
        if (ttl < MIN_TTL || ttl > MAX_TTL) {
            throw EnrollmentTokenValidationException("ttl", "must be between $MIN_TTL and $MAX_TTL inclusive")
        }
        if (label.length > LABEL_MAX_LENGTH) {
            throw EnrollmentTokenValidationException("label", "must be at most $LABEL_MAX_LENGTH characters")
        }
        return sessions.inTenant(tenantId) { issue(it, ttl, label, builtin = false) }
    }

    /**
     * A new built-in token for [tenantId] valid for [BUILTIN_TTL], in the same transaction that revokes the
     * tenant's other active built-in tokens: at most one is ever active.
     */
    fun replaceBuiltin(tenantId: UUID): IssuedEnrollmentToken =
        sessions.inTenant(tenantId) { session ->
            session.createMutationQuery(REVOKE_ACTIVE_BUILTIN).setParameter("now", clock.instant()).executeUpdate()
            issue(session, BUILTIN_TTL, "", builtin = true)
        }

    /**
     * Whether [tenantId] has an unused, not revoked built-in token whose secret hashes to [secretHash] and
     * which expires strictly later than [margin] from now.
     */
    fun builtinUsable(
        tenantId: UUID,
        secretHash: ByteArray,
        margin: Duration,
    ): Boolean =
        sessions.inTenant(tenantId) { session ->
            session
                .createSelectionQuery(USABLE_BUILTIN, java.lang.Long::class.java)
                .setParameter("hash", secretHash)
                .setParameter("limit", clock.instant() + margin)
                .singleResult
                .toLong() > 0
        }

    private fun issue(
        session: Session,
        ttl: Duration,
        label: String,
        builtin: Boolean,
    ): IssuedEnrollmentToken {
        val secret = EnrollmentSecret.random(random)
        val now = clock.instant()
        val record =
            EnrollmentTokenRecord(
                ids.next(),
                secret.hash(),
                expiresAt = now + ttl,
                createdAt = now,
                label = label,
                builtin = builtin,
            )
        session.persist(record)
        val token = EnrollmentToken(secret, ca.fingerprint()).encode()
        return IssuedEnrollmentToken(record.id, record.expiresAt, token, endpoint)
    }

    /**
     * Tokens of [tenantId] in [state] (all when null), newest first (decision 9), after [after] if
     * given, at most [limit]; completed tokens are kept forever.
     */
    fun list(
        tenantId: UUID,
        state: EnrollmentTokenState? = null,
        after: PageKey? = null,
        limit: Int = Int.MAX_VALUE,
    ): List<EnrollmentTokenSummary> =
        sessions.inTenant(tenantId) { session ->
            val hql = listQuery(state, after)
            val query = session.createSelectionQuery(hql, EnrollmentTokenRecord::class.java)
            if (":now" in hql) query.setParameter("now", clock.instant())
            after?.bind(query)
            query.setMaxResults(limit).list().map { summaryOf(it) }
        }

    private fun listQuery(
        state: EnrollmentTokenState?,
        after: PageKey?,
    ): String {
        val conditions =
            listOfNotNull("builtin = false", state?.let(::condition), after?.let { PageKey.condition("createdAt") })
        val where = hqlWhere(conditions)
        return "from EnrollmentTokenRecord $where order by createdAt desc, id desc"
    }

    /** The HQL of a state, the same as [EnrollmentTokenState.of] computes it. */
    private fun condition(state: EnrollmentTokenState): String =
        when (state) {
            EnrollmentTokenState.ACTIVE -> "usedAt is null and revokedAt is null and expiresAt > :now"
            EnrollmentTokenState.USED -> "usedAt is not null"
            EnrollmentTokenState.EXPIRED -> "usedAt is null and revokedAt is null and expiresAt <= :now"
            EnrollmentTokenState.REVOKED -> "usedAt is null and revokedAt is not null"
        }

    /** A single token of [tenantId] by id, or null if it does not exist in this tenant. */
    fun get(
        tenantId: UUID,
        id: UUID,
    ): EnrollmentTokenSummary? =
        sessions.inTenant(tenantId) { session ->
            session.find(EnrollmentTokenRecord::class.java, id)?.takeUnless { it.builtin }?.let { summaryOf(it) }
        }

    /**
     * Revokes [id] of [tenantId]: only an active token can be revoked (decision 6, S8b В1); a used or
     * an expired one is refused with the reason, a revoked one stays as it is and the call succeeds.
     * The update is guarded the same way as Enroll's claim, so a revoke racing a registration for the
     * same token is decided by whoever commits first, never by which one merely read the row first.
     */
    fun revoke(
        tenantId: UUID,
        id: UUID,
        now: Instant,
    ): RevokeResult =
        sessions.inTenant(tenantId) { session ->
            val revoked =
                session
                    .createMutationQuery(REVOKE)
                    .setParameter("now", now)
                    .setParameter("id", id)
                    .executeUpdate()
            if (revoked == 1) {
                RevokeResult.Revoked(now)
            } else {
                refusal(session.find(EnrollmentTokenRecord::class.java, id))
            }
        }

    /** The one system lookup of enrollment: by hash, across tenants, before the tenant is known. */
    fun ownerOf(tokenHash: ByteArray): TokenOwner? =
        sessions.system { session ->
            session
                .createSelectionQuery(BY_HASH, EnrollmentTokenRecord::class.java)
                .setParameter("hash", tokenHash)
                .uniqueResult()
                ?.let { TokenOwner(it.id, checkNotNull(it.tenantId), it.builtin) }
        }

    private fun summaryOf(record: EnrollmentTokenRecord) =
        EnrollmentTokenSummary(
            id = record.id,
            state = EnrollmentTokenState.of(record.usedAt, record.revokedAt, record.expiresAt, clock.instant()),
            createdAt = record.createdAt,
            expiresAt = record.expiresAt,
            label = record.label,
            usedAt = record.usedAt,
            agentId = record.agentId,
            revokedAt = record.revokedAt,
        )
}

/** Why the guarded update changed nothing; a built-in token is none of REST's business. */
private fun refusal(record: EnrollmentTokenRecord?): RevokeResult {
    val revokedAt = record?.revokedAt
    return when {
        record == null || record.builtin -> RevokeResult.NotFound
        record.usedAt != null -> RevokeResult.Rejected(RevokeRejection.USED)
        revokedAt != null -> RevokeResult.Revoked(revokedAt)
        else -> RevokeResult.Rejected(RevokeRejection.EXPIRED)
    }
}
