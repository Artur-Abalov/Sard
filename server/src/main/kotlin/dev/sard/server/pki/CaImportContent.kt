// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.pki

import org.bouncycastle.asn1.ASN1ObjectIdentifier
import org.bouncycastle.asn1.pkcs.PrivateKeyInfo
import org.bouncycastle.openssl.jcajce.JcaPEMKeyConverter
import java.io.ByteArrayInputStream
import java.nio.file.Path
import java.security.PrivateKey
import java.security.cert.CertificateFactory
import java.security.cert.X509Certificate

private const val SUPPORTED = "ECDSA P-256"
private val EC_PUBLIC_KEY = ASN1ObjectIdentifier("1.2.840.10045.2.1")
private val RSA_ENCRYPTION = ASN1ObjectIdentifier("1.2.840.113549.1.1.1")
private const val P256 = "1.2.840.10045.3.1.7"
private val CURVES = mapOf(P256 to "P-256", "1.3.132.0.34" to "P-384", "1.3.132.0.35" to "P-521")

/**
 * What the two files of a source hold. Every refusal names the file, never what is in it: a key placed
 * where the certificate belongs must not end up in an error message.
 */
internal class CaImportContent(
    private val source: Path,
    private val certPath: Path,
    private val keyPath: Path,
) {
    fun certificate(text: String): X509Certificate {
        val der = singlePemBlock("CERTIFICATE", text)
        return runCatching { x509(der) }.getOrNull()
            ?: throw refusal(CaImportRefusal.CA_CERT_INVALID, "$certPath is not exactly one X.509 certificate in PEM")
    }

    fun key(text: String): PrivateKey {
        val info = pkcs8(text)
        requireSupported(info)
        return runCatching { JcaPEMKeyConverter().getPrivateKey(info) }.getOrNull() ?: throw notPkcs8()
    }

    private fun pkcs8(text: String): PrivateKeyInfo =
        runCatching { PrivateKeyInfo.getInstance(singlePemBlock("PRIVATE KEY", text)) }.getOrNull() ?: throw notPkcs8()

    private fun requireSupported(info: PrivateKeyInfo) {
        val kind = describe(info)
        if (kind != SUPPORTED) {
            throw refusal(CaImportRefusal.CA_KEY_UNSUPPORTED, "$keyPath is $kind, only ECDSA P-256 is supported")
        }
    }

    private fun notPkcs8(): CaImportRefused =
        refusal(CaImportRefusal.CA_KEY_INVALID, "$keyPath is not an unencrypted PKCS#8 PEM key (BEGIN PRIVATE KEY)")

    fun requireMatch(
        certificate: X509Certificate,
        key: PrivateKey,
    ) {
        if (!Keys.matches(certificate, key)) {
            throw refusal(CaImportRefusal.CA_KEY_MISMATCH, "$keyPath is not the key of the certificate $certPath")
        }
    }

    private fun x509(der: ByteArray) =
        CertificateFactory.getInstance("X.509").generateCertificate(ByteArrayInputStream(der)) as X509Certificate

    private fun describe(info: PrivateKeyInfo): String {
        val algorithm = info.privateKeyAlgorithm
        return when (algorithm.algorithm) {
            RSA_ENCRYPTION -> "RSA"
            EC_PUBLIC_KEY -> "ECDSA ${CURVES[algorithm.parameters.toString()] ?: "curve ${algorithm.parameters}"}"
            else -> "algorithm ${algorithm.algorithm}"
        }
    }

    private fun refusal(
        reason: CaImportRefusal,
        detail: String,
    ): CaImportRefused = CaImportRefused(reason, detail, source)
}
