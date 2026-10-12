// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents

import dev.sard.server.pki.AgentIdentity
import dev.sard.server.pki.AgentRole
import dev.sard.server.pki.AgentRoleExtension
import dev.sard.server.pki.CertificateRole
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
    CERT_EXPIRED,
    AGENT_REVOKED,
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
    /** What its Sard role extension says (V1). */
    val role: CertificateRole,
) {
    companion object {
        fun of(certificate: X509Certificate): PresentedCertificate {
            val serial = certificate.serialNumber.toString(HEX)
            return PresentedCertificate(serial, AgentIdentity.of(certificate), AgentRoleExtension.read(certificate))
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

/**
 * Decides whether a presented certificate authenticates an agent (S3). No cache: every
 * call reads the record, so a revocation applies to the next call.
 */
class AgentAuthenticator(
    private val standings: CertificateStandings,
    private val clock: Clock,
) {
    fun authenticate(presented: PresentedCertificate?): AgentAuthResult =
        if (presented == null) {
            AgentAuthResult.Rejected(AgentAuthFailure.CERT_MISSING, serial = null, agentId = null)
        } else {
            judge(presented, standings.of(presented.serial))
        }

    private fun judge(
        presented: PresentedCertificate,
        standing: CertificateStanding?,
    ): AgentAuthResult {
        if (standing == null) {
            val claimed = presented.identity
            val failure = AgentAuthFailure.CERT_UNKNOWN
            return AgentAuthResult.Rejected(failure, presented.serial, claimed?.agentId, claimed?.tenantId)
        }
        val identity = standing.identity
        return when (val failure = failureOf(presented, standing)) {
            null -> {
                val principal = AgentPrincipal(identity.agentId, identity.tenantId, presented.serial, standing.role)
                AgentAuthResult.Accepted(principal)
            }

            // The role refusals are about the certificate, whose SAN matched the record: it may be named in the log.
            AgentAuthFailure.CERT_ROLE_MISSING, AgentAuthFailure.CERT_ROLE_INVALID -> {
                AgentAuthResult.Rejected(failure, presented.serial, identity.agentId, identity.tenantId)
            }

            else -> {
                AgentAuthResult.Rejected(failure, presented.serial, identity.agentId)
            }
        }
    }

    /** The certificate first (who it names, what role it says), then the state of the record (ADR 0009, V1). */
    private fun failureOf(
        presented: PresentedCertificate,
        standing: CertificateStanding,
    ): AgentAuthFailure? = certificateFailure(presented, standing) ?: stateFailure(standing)

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

    private fun stateFailure(standing: CertificateStanding): AgentAuthFailure? =
        when {
            standing.revokedAt != null -> AgentAuthFailure.CERT_REVOKED
            !clock.instant().isBefore(standing.notAfter) -> AgentAuthFailure.CERT_EXPIRED
            standing.agentRevokedAt != null -> AgentAuthFailure.AGENT_REVOKED
            else -> null
        }
}
