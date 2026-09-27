// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.pki

import java.security.PrivateKey
import java.security.cert.X509Certificate
import java.util.Base64

/** Minimal PEM: certificates and PKCS#8 private keys, 64-column base64. */
internal object Pem {
    private const val LINE = 64
    private val encoder = Base64.getMimeEncoder(LINE, "\n".toByteArray())

    fun certificate(certificate: X509Certificate) = encode("CERTIFICATE", certificate.encoded)

    fun privateKey(key: PrivateKey) = encode("PRIVATE KEY", key.encoded)

    fun decode(
        type: String,
        text: String,
    ): ByteArray {
        val body = text.substringAfter("-----BEGIN $type-----").substringBefore("-----END $type-----")
        return Base64.getMimeDecoder().decode(body)
    }

    private fun encode(
        type: String,
        der: ByteArray,
    ) = "-----BEGIN $type-----\n${encoder.encodeToString(der)}\n-----END $type-----\n"
}
