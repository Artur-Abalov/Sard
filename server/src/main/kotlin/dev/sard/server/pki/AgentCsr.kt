// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.pki

import org.bouncycastle.asn1.sec.SECObjectIdentifiers
import org.bouncycastle.asn1.x509.SubjectPublicKeyInfo
import org.bouncycastle.asn1.x9.X9ObjectIdentifiers
import org.bouncycastle.operator.jcajce.JcaContentVerifierProviderBuilder
import org.bouncycastle.pkcs.PKCS10CertificationRequest
import java.io.IOException

/** Agent keys the CA signs: ECDSA on P-256 (what sard-agent generates) or P-384. */
private val CURVES = setOf(SECObjectIdentifiers.secp256r1, SECObjectIdentifiers.secp384r1)

/** Validates an agent CSR; only its key is used, the rest of the request is ignored. */
internal object AgentCsr {
    /** The CSR's public key, once its type is supported and it proved possession by signing. */
    fun verifiedKey(der: ByteArray): SubjectPublicKeyInfo {
        val csr = parse(der)
        val key = csr.subjectPublicKeyInfo
        val algorithm = key.algorithm
        if (algorithm.algorithm != X9ObjectIdentifiers.id_ecPublicKey || algorithm.parameters !in CURVES) {
            throw InvalidCsrException("unsupported agent key ${algorithm.algorithm} ${algorithm.parameters}")
        }
        if (!csr.isSignatureValid(JcaContentVerifierProviderBuilder().build(key))) {
            throw InvalidCsrException("CSR signature does not verify")
        }
        return key
    }

    private fun parse(der: ByteArray): PKCS10CertificationRequest =
        try {
            PKCS10CertificationRequest(der)
        } catch (e: IOException) {
            throw InvalidCsrException("not a DER-encoded CSR", e)
        }
}
