// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.pki

import java.security.KeyPair
import java.security.KeyPairGenerator
import java.security.PrivateKey
import java.security.SecureRandom
import java.security.Signature
import java.security.cert.X509Certificate
import java.security.spec.ECGenParameterSpec
import java.time.Clock

/** ECDSA P-256 with SHA-256: every key the file CA generates, and how it signs. */
internal object Keys {
    const val SIGNATURE = "SHA256withECDSA"

    fun generate(random: SecureRandom): KeyPair =
        KeyPairGenerator.getInstance("EC").run {
            initialize(ECGenParameterSpec("secp256r1"), random)
            generateKeyPair()
        }

    /** True when [key] signs what [certificate]'s public key verifies; false for a key of another kind. */
    fun matches(
        certificate: X509Certificate,
        key: PrivateKey,
    ): Boolean =
        runCatching {
            val probe = certificate.encoded
            val signed =
                Signature.getInstance(SIGNATURE).run {
                    initSign(key)
                    update(probe)
                    sign()
                }
            Signature.getInstance(SIGNATURE).run {
                initVerify(certificate.publicKey)
                update(probe)
                verify(signed)
            }
        }.getOrDefault(false)
}

/** The root certificate with its private key; refuses anything that is not a self-signed CA holding that key. */
class CaKeyPair(
    val certificate: X509Certificate,
    internal val privateKey: PrivateKey,
) {
    init {
        val name = certificate.subjectX500Principal
        check(selfSigned()) { "CA certificate $name is not self-signed" }
        check(certificate.basicConstraints >= 0) { "CA certificate $name is not a CA" }
        check(Keys.matches(certificate, privateKey)) { "CA key does not match the CA certificate $name" }
    }

    private fun selfSigned() = runCatching { certificate.verify(certificate.publicKey) }.isSuccess

    /** Never prints the key. */
    override fun toString() = "CaKeyPair(${certificate.subjectX500Principal})"

    companion object {
        fun generate(
            clock: Clock,
            random: SecureRandom,
        ): CaKeyPair {
            val keys = Keys.generate(random)
            return CaKeyPair(Certificates.root(keys, clock.instant(), random), keys.private)
        }
    }
}
