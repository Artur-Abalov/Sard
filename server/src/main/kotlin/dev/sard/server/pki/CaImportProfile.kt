// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.pki

import java.nio.file.Path
import java.security.cert.X509Certificate
import java.time.Instant

private const val KEY_CERT_SIGN = 5

/** What makes a certificate a CA Sard accepts (ADR 0052); the refusals name [certPath], never its content. */
internal class CaImportProfile(
    private val source: Path,
    private val certPath: Path,
) {
    /** Self-signed, `CA:TRUE`, `keyCertSign` if the extension is there, valid at [now], in this order. */
    fun requireUsableCa(
        certificate: X509Certificate,
        now: Instant,
    ) {
        requireSelfSignedCa(certificate)
        requireKeyCertSign(certificate)
        requireValidAt(certificate, now)
    }

    private fun requireSelfSignedCa(certificate: X509Certificate) {
        val subject = certificate.subjectX500Principal
        if (runCatching { certificate.verify(certificate.publicKey) }.isFailure) {
            val issuer = certificate.issuerX500Principal
            throw refusal(CaImportRefusal.CA_NOT_SELF_SIGNED, "$certPath: subject $subject, issuer $issuer")
        }
        if (certificate.basicConstraints < 0) {
            throw refusal(CaImportRefusal.CA_NOT_A_CA, "$certPath: $subject has no basicConstraints CA:TRUE")
        }
    }

    private fun requireKeyCertSign(certificate: X509Certificate) {
        if (certificate.keyUsage?.get(KEY_CERT_SIGN) == false) {
            val subject = certificate.subjectX500Principal
            throw refusal(CaImportRefusal.CA_KEY_USAGE, "$certPath: keyUsage of $subject lacks keyCertSign")
        }
    }

    private fun requireValidAt(
        certificate: X509Certificate,
        now: Instant,
    ) {
        val notBefore = certificate.notBefore.toInstant()
        val notAfter = certificate.notAfter.toInstant()
        if (now < notBefore) {
            val detail = "$certPath: notBefore is $notBefore, the server clock shows $now"
            throw refusal(CaImportRefusal.CA_NOT_YET_VALID, detail)
        }
        if (now >= notAfter) throw refusal(CaImportRefusal.CA_EXPIRED, "$certPath: notAfter is $notAfter")
    }

    private fun refusal(
        reason: CaImportRefusal,
        detail: String,
    ): CaImportRefused = CaImportRefused(reason, detail, source)
}
