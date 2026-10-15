// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents

import dev.sard.server.pki.AgentIdentity
import dev.sard.server.pki.AgentRoleUri
import dev.sard.server.pki.CertificateRole
import dev.sard.server.role.AgentRole
import io.grpc.Context
import java.security.cert.X509Certificate
import java.time.Clock
import java.time.Instant
import java.util.UUID

private const val HEX = 16

/** Why an agent call is refused (ADR 0009); the name is the ErrorInfo reason. */
enum class AgentAuthFailure {
    CERT_MISSING,
    CERT_UNKNOWN,
    CERT_IDENTITY_MISMATCH,
    CERT_ROLE_MISSING,
    CERT_ROLE_INVALID,
    CERT_REVOKED,
    CERT_SUPERSEDED,
    CERT_NOT_CONFIRMED,
    CERT_EXPIRED,
    AGENT_REVOKED,
}

/**
 * What a judgement is for. A new call is judged by every check; the confirmation of a renewed certificate
 * is the one call an unconfirmed certificate may make (C1, П8); an open stream is never closed because its
 * certificate was superseded or not yet confirmed (C1, П11) and is judged only by revocation and expiry.
 */
enum class AuthScope {
    CALL,
    CONFIRM,
    OPEN_STREAM,
}

/** The agent a call is authenticated as, for as long as the call (or stream) lasts. */
data class AgentPrincipal(
    val agentId: UUID,
    val tenantId: UUID,
    val serial: String,
    /** From the agents table, which equals the one in the certificate (V1): a source or a verifier. */
    val role: AgentRole,
) {
    companion object {
        /** Where [AgentAuthInterceptor] puts the principal: the call's gRPC [Context]. */
        val KEY: Context.Key<AgentPrincipal> = Context.key("sard.agent-principal")
    }
}

/** What the TLS layer vouches for: a certificate chaining to the Sard CA. */
data class PresentedCertificate(
    /** Lower-case hex, as `agent_certificates.serial` stores it. */
    val serial: String,
    /** The identity in its URI SAN, or null when it names none. */
    val identity: AgentIdentity?,
    /** What its role URI says (V1). */
    val role: CertificateRole,
) {
    companion object {
        fun of(certificate: X509Certificate): PresentedCertificate {
            val serial = certificate.serialNumber.toString(HEX)
            return PresentedCertificate(serial, AgentIdentity.of(certificate), AgentRoleUri.read(certificate))
        }
    }
}

/** What the server has on record for a certificate serial. */
data class CertificateStanding(
    val identity: AgentIdentity,
    val notAfter: Instant,
    val revokedAt: Instant?,
    val agentRevokedAt: Instant?,
    val role: AgentRole,
    /** Set when a confirmed renewal, or a later renewal, replaced this certificate (C1, П7). */
    val supersededAt: Instant? = null,
    /** Issued by a renewal that nothing has confirmed or replaced yet (C1, П7). */
    val unconfirmed: Boolean = false,
)

/** Looks a certificate up by serial across tenants: the tenant is what it establishes. */
fun interface CertificateStandings {
    fun of(serial: String): CertificateStanding?
}

/** Certificate records for many serials in one lookup, by serial (S5a: open sessions). */
fun interface BatchStandings {
    fun of(serials: Collection<String>): Map<String, CertificateStanding>
}

sealed interface AgentAuthResult {
    data class Accepted(
        val principal: AgentPrincipal,
    ) : AgentAuthResult

    /** [serial], [agentId] and [tenantId] are only for the log; the caller sees [failure] alone. */
    data class Rejected(
        val failure: AgentAuthFailure,
        val serial: String?,
        val agentId: UUID?,
        val tenantId: UUID? = null,
    ) : AgentAuthResult
}

private val ROLE_REFUSALS = setOf(AgentAuthFailure.CERT_ROLE_MISSING, AgentAuthFailure.CERT_ROLE_INVALID)

/**
 * Decides whether a presented certificate authenticates an agent (S3). No cache: every
 * call reads the record, so a revocation applies to the next call.
 */
class AgentAuthenticator(
    private val standings: CertificateStandings,
    private val clock: Clock,
) {
    fun authenticate(
        presented: PresentedCertificate?,
        scope: AuthScope = AuthScope.CALL,
    ): AgentAuthResult =
        if (presented == null) {
            AgentAuthResult.Rejected(AgentAuthFailure.CERT_MISSING, serial = null, agentId = null)
        } else {
            judge(presented, standings.of(presented.serial), scope)
        }

    private fun judge(
        presented: PresentedCertificate,
        standing: CertificateStanding?,
        scope: AuthScope,
    ): AgentAuthResult {
        if (standing == null) return unknown(presented)
        val failure = failureOf(presented, standing, scope)
        return if (failure == null) accepted(presented, standing) else rejected(failure, presented, standing)
    }

    private fun unknown(presented: PresentedCertificate): AgentAuthResult {
        val claimed = presented.identity
        val failure = AgentAuthFailure.CERT_UNKNOWN
        return AgentAuthResult.Rejected(failure, presented.serial, claimed?.agentId, claimed?.tenantId)
    }

    private fun rejected(
        failure: AgentAuthFailure,
        presented: PresentedCertificate,
        standing: CertificateStanding,
    ): AgentAuthResult {
        val identity = standing.identity
        // The role refusals are about the certificate, whose SAN matched the record: it may be named in the log.
        val tenantId = if (failure in ROLE_REFUSALS) identity.tenantId else null
        return AgentAuthResult.Rejected(failure, presented.serial, identity.agentId, tenantId)
    }

    private fun accepted(
        presented: PresentedCertificate,
        standing: CertificateStanding,
    ): AgentAuthResult {
        val identity = standing.identity
        val principal = AgentPrincipal(identity.agentId, identity.tenantId, presented.serial, standing.role)
        return AgentAuthResult.Accepted(principal)
    }

    /** The certificate first (who it names, what role it says), then the state of the record (ADR 0009, V1). */
    private fun failureOf(
        presented: PresentedCertificate,
        standing: CertificateStanding,
        scope: AuthScope,
    ): AgentAuthFailure? = certificateFailure(presented, standing) ?: stateFailure(standing, scope)

    private fun certificateFailure(
        presented: PresentedCertificate,
        standing: CertificateStanding,
    ): AgentAuthFailure? =
        when {
            presented.identity != standing.identity -> AgentAuthFailure.CERT_IDENTITY_MISMATCH
            presented.role == CertificateRole.Missing -> AgentAuthFailure.CERT_ROLE_MISSING
            presented.role == CertificateRole.Invalid -> AgentAuthFailure.CERT_ROLE_INVALID
            presented.role != CertificateRole.Valid(standing.role) -> AgentAuthFailure.CERT_IDENTITY_MISMATCH
            else -> null
        }

    private fun stateFailure(
        standing: CertificateStanding,
        scope: AuthScope,
    ): AgentAuthFailure? =
        when {
            standing.revokedAt != null -> AgentAuthFailure.CERT_REVOKED
            standing.supersededAt != null && scope != AuthScope.OPEN_STREAM -> AgentAuthFailure.CERT_SUPERSEDED
            standing.unconfirmed && scope == AuthScope.CALL -> AgentAuthFailure.CERT_NOT_CONFIRMED
            !clock.instant().isBefore(standing.notAfter) -> AgentAuthFailure.CERT_EXPIRED
            standing.agentRevokedAt != null -> AgentAuthFailure.AGENT_REVOKED
            else -> null
        }
}
