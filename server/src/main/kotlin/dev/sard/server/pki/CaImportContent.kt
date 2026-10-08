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
import java.time.Instant
import java.util.Base64

private const val KEY_CERT_SIGN = 5
private const val SUPPORTED = "ECDSA P-256"
private const val BEGIN = "-----BEGIN"
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
        val der = single("CERTIFICATE", text)
        return runCatching { x509(der) }.getOrNull()
            ?: refuse(CaImportRefusal.CA_CERT_INVALID, "$certPath is not exactly one X.509 certificate in PEM")
    }

    fun key(text: String): PrivateKey {
        val info =
            runCatching { PrivateKeyInfo.getInstance(single("PRIVATE KEY", text)) }.getOrNull()
                ?: refuse(CaImportRefusal.CA_KEY_INVALID, "$keyPath is not an unencrypted PKCS#8 PEM key (BEGIN PRIVATE KEY)")
        val kind = describe(info)
        if (kind != SUPPORTED) {
            refuse(CaImportRefusal.CA_KEY_UNSUPPORTED, "$keyPath is $kind, only ECDSA P-256 is supported")
        }
        return runCatching { JcaPEMKeyConverter().getPrivateKey(info) }.getOrNull()
            ?: refuse(CaImportRefusal.CA_KEY_INVALID, "$keyPath is not an unencrypted PKCS#8 PEM key (BEGIN PRIVATE KEY)")
    }

    fun requireMatch(
        certificate: X509Certificate,
        key: PrivateKey,
    ) {
        if (!Keys.matches(certificate, key)) {
            refuse(CaImportRefusal.CA_KEY_MISMATCH, "$keyPath is not the key of the certificate $certPath")
        }
    }

    /** Self-signed, `CA:TRUE`, `keyCertSign` if the extension is there, valid at [now], in this order. */
    fun requireUsableCa(
        certificate: X509Certificate,
        now: Instant,
    ) {
        val subject = certificate.subjectX500Principal
        if (runCatching { certificate.verify(certificate.publicKey) }.isFailure) {
            refuse(CaImportRefusal.CA_NOT_SELF_SIGNED, "$certPath: subject $subject, issuer ${certificate.issuerX500Principal}")
        }
        if (certificate.basicConstraints < 0) {
            refuse(CaImportRefusal.CA_NOT_A_CA, "$certPath: $subject has no basicConstraints CA:TRUE")
        }
        if (certificate.keyUsage?.get(KEY_CERT_SIGN) == false) {
            refuse(CaImportRefusal.CA_KEY_USAGE, "$certPath: keyUsage of $subject lacks keyCertSign")
        }
        requireValidAt(certificate, now)
    }

    private fun requireValidAt(
        certificate: X509Certificate,
        now: Instant,
    ) {
        val notBefore = certificate.notBefore.toInstant()
        val notAfter = certificate.notAfter.toInstant()
        if (now < notBefore) {
            refuse(CaImportRefusal.CA_NOT_YET_VALID, "$certPath: notBefore is $notBefore, the server clock shows $now")
        }
        if (now >= notAfter) refuse(CaImportRefusal.CA_EXPIRED, "$certPath: notAfter is $notAfter")
    }

    private fun x509(der: ByteArray) =
        CertificateFactory.getInstance("X.509").generateCertificate(ByteArrayInputStream(der)) as X509Certificate

    /** The DER of the one PEM block of [type] in [text]; null when there is not exactly one block and nothing else. */
    private fun single(
        type: String,
        text: String,
    ): ByteArray {
        val blocks = Regex("$BEGIN $type-----(.*?)-----END $type-----", RegexOption.DOT_MATCHES_ALL).findAll(text).toList()
        val only = blocks.singleOrNull()?.takeIf { text.split(BEGIN).size == 2 }
        return only?.let { runCatching { Base64.getMimeDecoder().decode(it.groupValues[1]) }.getOrNull() } ?: ByteArray(0)
    }

    private fun describe(info: PrivateKeyInfo): String {
        val algorithm = info.privateKeyAlgorithm
        return when (algorithm.algorithm) {
            RSA_ENCRYPTION -> "RSA"
            EC_PUBLIC_KEY -> "ECDSA ${CURVES[algorithm.parameters.toString()] ?: "curve ${algorithm.parameters}"}"
            else -> "algorithm ${algorithm.algorithm}"
        }
    }

    private fun refuse(
        reason: CaImportRefusal,
        detail: String,
    ): Nothing = throw CaImportRefused(reason, detail, source)
}
