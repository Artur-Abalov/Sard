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
import dev.sard.server.pki.IssuedCertificate
import org.hibernate.Session
import java.security.cert.CertificateFactory
import java.security.cert.X509Certificate
import java.time.Clock
import java.time.Instant
import java.util.UUID

/** Marks the token used, but only if nobody has and it has not expired; the row lock serialises racers. */
private const val CLAIM =
    "update EnrollmentTokenRecord set usedAt = :now where id = :id and usedAt is null and expiresAt > :now"
private const val BIND_AGENT = "update EnrollmentTokenRecord set agentId = :agent where id = :id"
private const val SERIAL_RADIX = 16

/** What Enroll hands back: the agent's id, its certificate chain and the CA bundle to trust. */
class EnrolledAgent(
    val agentId: UUID,
    val certificateChainPem: String,
    val caBundlePem: String,
)

/** The token cannot enroll anyone; the reason is for the interim status mapping, not for users. */
class EnrollmentRejectedException(
    val reason: Reason,
    cause: Throwable? = null,
) : RuntimeException("enrollment token rejected: ${reason.name.lowercase()}", cause) {
    enum class Reason { MALFORMED_TOKEN, FOREIGN_CA, UNKNOWN_TOKEN, USED_TOKEN, EXPIRED_TOKEN }
}

/**
 * Enroll (ADR 0009): trades a one-time token and a CSR for an agent in the token's
 * tenant and its client certificate. One transaction: on any failure the token stays
 * unused and neither the agent nor the certificate exists.
 */
class Enrollment(
    private val sessions: TenantSessions,
    private val tokens: EnrollmentTokens,
    private val ca: CertificateAuthority,
    private val clock: Clock,
    private val ids: UuidV7,
) {
    fun enroll(
        token: String,
        csrDer: ByteArray,
        hostname: String,
    ): EnrolledAgent {
        val parsed = parse(token)
        if (parsed.fingerprint != ca.fingerprint()) throw EnrollmentRejectedException(Reason.FOREIGN_CA)
        val owner = tokens.ownerOf(parsed.secret.hash()) ?: throw EnrollmentRejectedException(Reason.UNKNOWN_TOKEN)
        return sessions.inTenant(owner.tenantId) { session -> enrollIn(session, owner, csrDer, hostname) }
    }

    private fun parse(token: String) =
        try {
            EnrollmentToken.parse(token)
        } catch (e: MalformedEnrollmentTokenException) {
            throw EnrollmentRejectedException(Reason.MALFORMED_TOKEN, e)
        }

    private fun enrollIn(
        session: Session,
        owner: TokenOwner,
        csrDer: ByteArray,
        hostname: String,
    ): EnrolledAgent {
        val now = clock.instant()
        claim(session, owner.tokenId, now)
        val agent = Agent(ids.next(), hostname, agentVersion = null, registeredAt = now, lastSeenAt = null)
        session.persist(agent)
        session.flush()
        val issued = ca.issueAgentCertificate(csrDer, AgentIdentity(owner.tenantId, agent.id))
        session.persist(certificateRecord(agent.id, issued))
        session
            .createMutationQuery(BIND_AGENT)
            .setParameter("agent", agent.id)
            .setParameter("id", owner.tokenId)
            .executeUpdate()
        return EnrolledAgent(agent.id, issued.chainPem, ca.caBundlePem())
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
                token == null -> Reason.UNKNOWN_TOKEN
                token.usedAt != null -> Reason.USED_TOKEN
                else -> Reason.EXPIRED_TOKEN
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
}

private typealias Reason = EnrollmentRejectedException.Reason
