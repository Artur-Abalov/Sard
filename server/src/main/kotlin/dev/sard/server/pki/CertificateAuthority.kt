// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.pki

import org.bouncycastle.cert.jcajce.JcaX509CertificateHolder
import java.math.BigInteger
import java.security.MessageDigest
import java.security.cert.X509Certificate
import java.time.Instant
import java.util.HexFormat
import java.util.UUID
import javax.net.ssl.X509KeyManager

/**
 * Issues and anchors the certificates of the agent transport (ADR 0014). Implies no
 * algorithm and no key store: the open core keeps its CA in files, an enterprise
 * starter may use Vault, PKCS#11 or a GOST provider behind the same interface.
 */
interface CertificateAuthority {
    /** PEM of the trust anchors agents verify the server against. */
    fun caBundlePem(): String

    /** Pins the root; the agent compares it with the one in its enrollment token. */
    fun fingerprint(): CaFingerprint

    /** Where this CA came from, as recorded when it was created or imported (F4a, Р12). */
    fun provenance(): CaProvenance

    /** Where the root key is kept, as an operator finds it: an absolute file path for the open core. */
    fun keyLocation(): String

    /** Key material the gRPC listener serves; the key itself may never leave its store. */
    fun serverKeyManager(): X509KeyManager

    /**
     * Replaces the server certificate behind [serverKeyManager] if it is due, without a
     * restart. Returns true when it did. Called periodically; a CA whose server
     * certificate is managed elsewhere (corporate PKI) returns false.
     */
    fun renewServerCertificate(): Boolean

    /**
     * Signs an agent CSR (DER): a client-only certificate naming [agent], whatever the
     * CSR asks for. Throws [InvalidCsrException] for a bad signature or an unsupported key.
     */
    fun issueAgentCertificate(
        csrDer: ByteArray,
        agent: AgentIdentity,
    ): IssuedCertificate
}

private const val SAN_URI = 6
private const val UUID_PATTERN = "[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}"
private val AGENT_URI = Regex("sard://tenants/($UUID_PATTERN)/agents/($UUID_PATTERN)")

/** Who a certificate is issued to; both ids end up in its URI SAN. */
data class AgentIdentity(
    val tenantId: UUID,
    val agentId: UUID,
) {
    /** The URI SAN of this identity (ADR 0014). */
    fun uri(): String = "sard://tenants/$tenantId/agents/$agentId"

    companion object {
        /** The identity [uri] names, or null unless it is exactly an agent URI. */
        fun parse(uri: String): AgentIdentity? =
            AGENT_URI.matchEntire(uri)?.destructured?.let { (tenant, agent) ->
                AgentIdentity(UUID.fromString(tenant), UUID.fromString(agent))
            }

        /** The identity in [certificate]'s URI SAN, or null when it carries no agent URI. */
        fun of(certificate: X509Certificate): AgentIdentity? =
            certificate.subjectAlternativeNames
                .orEmpty()
                .filter { it[0] == SAN_URI }
                .firstNotNullOfOrNull { parse(it[1] as String) }
    }
}

/** An agent certificate: the chain up to (not including) the root, plus what S2 records. */
data class IssuedCertificate(
    val chainPem: String,
    val serial: BigInteger,
    val notAfter: Instant,
)

/** SHA-256 of the root's DER SubjectPublicKeyInfo, lower-case hex (ADR 0014). */
@JvmInline
value class CaFingerprint(
    val hex: String,
) {
    companion object {
        fun of(certificate: X509Certificate): CaFingerprint {
            val spki = JcaX509CertificateHolder(certificate).subjectPublicKeyInfo.encoded
            return CaFingerprint(HexFormat.of().formatHex(MessageDigest.getInstance("SHA-256").digest(spki)))
        }
    }
}

/** The CSR cannot be turned into an agent certificate. */
class InvalidCsrException(
    message: String,
    cause: Throwable? = null,
) : IllegalArgumentException(message, cause)
