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
        check(matches()) { "CA key does not match the CA certificate $name" }
    }

    private fun selfSigned() = runCatching { certificate.verify(certificate.publicKey) }.isSuccess

    private fun matches(): Boolean {
        val probe = certificate.encoded
        val signed =
            Signature.getInstance(Keys.SIGNATURE).run {
                initSign(privateKey)
                update(probe)
                sign()
            }
        return Signature.getInstance(Keys.SIGNATURE).run {
            initVerify(certificate.publicKey)
            update(probe)
            verify(signed)
        }
    }

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
