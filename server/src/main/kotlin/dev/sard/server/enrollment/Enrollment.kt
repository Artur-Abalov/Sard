// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.enrollment

import dev.sard.server.persistence.Agent
import dev.sard.server.persistence.AgentCertificateRecord
import dev.sard.server.persistence.EnrollmentTokenRecord
import dev.sard.server.persistence.TenantSessions
import dev.sard.server.persistence.UuidV7
import dev.sard.server.pki.AgentIdentity
import dev.sard.server.pki.CertificateAuthority
import dev.sard.server.pki.InvalidCsrException
import dev.sard.server.pki.IssuedCertificate
import org.hibernate.Session
import java.security.cert.CertificateFactory
import java.security.cert.X509Certificate
import java.time.Clock
import java.time.Instant
import java.util.UUID
import kotlin.coroutines.cancellation.CancellationException

/** Marks the token used, unless it is used, revoked or expired already; the row lock serialises racers. */
private const val CLAIM =
    "update EnrollmentTokenRecord set usedAt = :now where id = :id " +
        "and usedAt is null and revokedAt is null and expiresAt > :now"
private const val BIND_AGENT = "update EnrollmentTokenRecord set agentId = :agent where id = :id"
private const val SERIAL_RADIX = 16
private const val HOSTNAME_MAX_LENGTH = 253

/** What Enroll hands back: the agent's id, its certificate chain and the CA bundle to trust. */
class EnrolledAgent(
    val agentId: UUID,
    val certificateChainPem: String,
    val caBundlePem: String,
)

/**
 * The token cannot enroll anyone (rejection contract, docs/specs/server/agent-enrollment.feature).
 * The message never names the reason's private detail (a cause, if any, carries it for the log only).
 */
class EnrollmentRejectedException(
    val reason: Reason,
    cause: Throwable? = null,
) : RuntimeException("enrollment rejected: ${reason.name}", cause) {
    /** Order matches the rejection contract table: the first check that fails decides the answer. */
    enum class Reason {
        TOKEN_MALFORMED,
        TOKEN_FOREIGN_CA,
        TOKEN_UNKNOWN,
        TOKEN_USED,
        TOKEN_REVOKED,
        TOKEN_EXPIRED,
        HOSTNAME_INVALID,
        CSR_INVALID,
        INTERNAL_RETRYABLE,
    }
}

/**
 * Enroll (ADR 0009): trades a one-time token and a CSR for an agent in the token's
 * tenant and its client certificate. One transaction: on any failure the token stays
 * as it was and neither the agent nor the certificate exists.
 */
class Enrollment(
    private val sessions: TenantSessions,
    private val tokens: EnrollmentTokens,
    private val ca: CertificateAuthority,
    private val clock: Clock,
    private val ids: UuidV7,
) {
    /**
     * [cancelled] is polled once, right after the CSR is signed and before anything commits
     * (decision 7): true there rolls the transaction back without spending the token.
     */
    fun enroll(
        token: String,
        csrDer: ByteArray,
        hostname: String,
        cancelled: () -> Boolean = { false },
    ): EnrolledAgent {
        val parsed = parse(token)
        if (parsed.fingerprint != ca.fingerprint()) throw EnrollmentRejectedException(Reason.TOKEN_FOREIGN_CA)
        val owner = lookUp(parsed) ?: throw EnrollmentRejectedException(Reason.TOKEN_UNKNOWN)
        return runInTransaction(owner, csrDer, hostname, cancelled)
    }

    private fun parse(token: String) =
        try {
            EnrollmentToken.parse(token)
        } catch (e: MalformedEnrollmentTokenException) {
            throw EnrollmentRejectedException(Reason.TOKEN_MALFORMED, e)
        }

    private fun lookUp(parsed: EnrollmentToken): TokenOwner? = wrapUnexpected { tokens.ownerOf(parsed.secret.hash()) }

    private fun runInTransaction(
        owner: TokenOwner,
        csrDer: ByteArray,
        hostname: String,
        cancelled: () -> Boolean,
    ): EnrolledAgent =
        wrapUnexpected {
            sessions.inTenant(owner.tenantId) { session -> enrollIn(session, owner, csrDer, hostname, cancelled) }
        }

    private fun enrollIn(
        session: Session,
        owner: TokenOwner,
        csrDer: ByteArray,
        hostname: String,
        cancelled: () -> Boolean,
    ): EnrolledAgent {
        val now = clock.instant()
        claim(session, owner.tokenId, now)
        validateHostname(hostname)
        val agent = Agent(ids.next(), hostname, agentVersion = null, registeredAt = now, lastSeenAt = null)
        session.persist(agent)
        session.flush()
        val issued = issueCertificate(csrDer, AgentIdentity(owner.tenantId, agent.id))
        // Decision 7: past this point a client cancellation no longer stops the commit.
        if (cancelled()) throw CancellationException()
        session.persist(certificateRecord(agent.id, issued))
        session
            .createMutationQuery(BIND_AGENT)
            .setParameter("agent", agent.id)
            .setParameter("id", owner.tokenId)
            .executeUpdate()
        return EnrolledAgent(agent.id, issued.chainPem, ca.caBundlePem())
    }

    private fun issueCertificate(
        csrDer: ByteArray,
        identity: AgentIdentity,
    ): IssuedCertificate =
        try {
            ca.issueAgentCertificate(csrDer, identity)
        } catch (e: InvalidCsrException) {
            throw EnrollmentRejectedException(Reason.CSR_INVALID, e)
        }

    private fun validateHostname(hostname: String) {
        if (hostname.isEmpty() || hostname.length > HOSTNAME_MAX_LENGTH) {
            throw EnrollmentRejectedException(Reason.HOSTNAME_INVALID)
        }
    }

    private fun claim(
        session: Session,
        tokenId: UUID,
        now: Instant,
    ) {
        val claimed =
            session
                .createMutationQuery(CLAIM)
                .setParameter("now", now)
                .setParameter("id", tokenId)
                .executeUpdate()
        if (claimed == 1) return
        val token = session.find(EnrollmentTokenRecord::class.java, tokenId)
        val reason =
            when {
                token == null -> Reason.TOKEN_UNKNOWN
                token.usedAt != null -> Reason.TOKEN_USED
                token.revokedAt != null -> Reason.TOKEN_REVOKED
                else -> Reason.TOKEN_EXPIRED
            }
        throw EnrollmentRejectedException(reason)
    }

    private fun certificateRecord(
        agentId: UUID,
        issued: IssuedCertificate,
    ): AgentCertificateRecord {
        val leaf = CertificateFactory.getInstance("X.509").generateCertificate(issued.chainPem.byteInputStream())
        val issuedAt = (leaf as X509Certificate).notBefore.toInstant()
        return AgentCertificateRecord(issued.serial.toString(SERIAL_RADIX), agentId, issuedAt, issued.notAfter)
    }

    /**
     * The one place that catches whatever a collaborator (Hibernate, the CA, ...) might throw and
     * turns it into INTERNAL_RETRYABLE (rejection contract): the boundary is deliberately generic,
     * because anything not already an [EnrollmentRejectedException] here is, by definition, unexpected.
     */
    @Suppress("TooGenericExceptionCaught")
    private fun <T> wrapUnexpected(block: () -> T): T =
        try {
            block()
        } catch (e: EnrollmentRejectedException) {
            throw e
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            throw EnrollmentRejectedException(Reason.INTERNAL_RETRYABLE, e)
        }
}

private typealias Reason = EnrollmentRejectedException.Reason
