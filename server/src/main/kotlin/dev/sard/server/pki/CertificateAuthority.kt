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

    /** Key material the gRPC listener serves; the key itself may never leave its store. */
    fun serverKeyManager(): X509KeyManager

    /**
     * Signs an agent CSR (DER): a client-only certificate naming [agent], whatever the
     * CSR asks for. Throws [InvalidCsrException] for a bad signature or an unsupported key.
     */
    fun issueAgentCertificate(
        csrDer: ByteArray,
        agent: AgentIdentity,
    ): IssuedCertificate
}

/** Who a certificate is issued to; both ids end up in its URI SAN. */
data class AgentIdentity(
    val tenantId: UUID,
    val agentId: UUID,
)

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
