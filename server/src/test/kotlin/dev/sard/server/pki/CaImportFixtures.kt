// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.pki

import org.bouncycastle.asn1.x500.X500Name
import org.bouncycastle.asn1.x509.BasicConstraints
import org.bouncycastle.asn1.x509.Extension
import org.bouncycastle.asn1.x509.KeyUsage
import org.bouncycastle.asn1.x509.SubjectPublicKeyInfo
import org.bouncycastle.cert.X509v3CertificateBuilder
import org.bouncycastle.cert.jcajce.JcaX509CertificateConverter
import org.bouncycastle.operator.jcajce.JcaContentSignerBuilder
import java.math.BigInteger
import java.nio.file.Files
import java.nio.file.Path
import java.nio.file.attribute.PosixFilePermissions
import java.security.KeyPair
import java.security.KeyPairGenerator
import java.security.PrivateKey
import java.security.cert.X509Certificate
import java.security.spec.ECGenParameterSpec
import java.time.Clock
import java.time.Instant
import java.time.ZoneOffset
import java.util.Date

/** What a test needs to build import sources: a fixed clock and certificates with one property changed. */
object CaImportFixtures {
    /** The server clock of the F8 scenarios. */
    val NOW: Instant = Instant.parse("2026-10-08T12:00:00Z")
    val CLOCK: Clock = Clock.fixed(NOW, ZoneOffset.UTC)

    private val ISSUED: Instant = Instant.parse("2026-01-01T00:00:00Z")
    private val TEN_YEARS: Instant = Instant.parse("2036-01-01T00:00:00Z")

    /** The CA a file CA generated on 2026-01-01 (server A). */
    fun original(): CaKeyPair = CaKeyPair.generate(Clock.fixed(ISSUED, ZoneOffset.UTC), PkiFixtures.random())

    fun p256(): KeyPair = Keys.generate(PkiFixtures.random())

    fun p384(): KeyPair = generate("EC", ECGenParameterSpec("secp384r1"))

    fun rsa(): KeyPair =
        KeyPairGenerator.getInstance("RSA").run {
            initialize(2048)
            generateKeyPair()
        }

    private fun generate(
        algorithm: String,
        spec: ECGenParameterSpec,
    ): KeyPair =
        KeyPairGenerator.getInstance(algorithm).run {
            initialize(spec)
            generateKeyPair()
        }

    /** The properties of a test certificate; [basicConstraints] and [keyUsage] null omit their extension. */
    data class Profile(
        val subject: String = "CN=Sard CA",
        val issuer: String = subject,
        val signer: PrivateKey? = null,
        val notBefore: Instant = ISSUED,
        val notAfter: Instant = TEN_YEARS,
        val basicConstraints: Boolean? = true,
        val keyUsage: Int? = KeyUsage.keyCertSign or KeyUsage.cRLSign,
    )

    /** A certificate for [keys], signed by [Profile.signer] (default: its own key). */
    fun certificate(
        keys: KeyPair,
        profile: Profile = Profile(),
    ): X509Certificate {
        val spki = SubjectPublicKeyInfo.getInstance(keys.public.encoded)
        val builder =
            X509v3CertificateBuilder(
                X500Name(profile.issuer),
                BigInteger.valueOf(42),
                Date.from(profile.notBefore),
                Date.from(profile.notAfter),
                X500Name(profile.subject),
                spki,
            )
        profile.basicConstraints?.let { builder.addExtension(Extension.basicConstraints, true, BasicConstraints(it)) }
        profile.keyUsage?.let { builder.addExtension(Extension.keyUsage, true, KeyUsage(it)) }
        val signer = profile.signer ?: keys.private
        val algorithm = if (signer.algorithm == "RSA") "SHA256withRSA" else "SHA256withECDSA"
        return JcaX509CertificateConverter().getCertificate(builder.build(JcaContentSignerBuilder(algorithm).build(signer)))
    }

    /** `<root>/ca/{ca.crt,ca.key}` with the permissions the server demands: 0700 directories, 0600 files. */
    fun source(
        root: Path,
        certificatePem: String,
        keyPem: String,
    ): Path {
        Files.createDirectories(root.resolve("ca"))
        Files.writeString(root.resolve("ca/ca.crt"), certificatePem)
        Files.writeString(root.resolve("ca/ca.key"), keyPem)
        seal(root)
        return root
    }

    fun source(
        root: Path,
        pair: CaKeyPair,
    ): Path = source(root, Pem.certificate(pair.certificate), Pem.privateKey(pair.privateKey))

    /** Owner-only on the source root, `ca` and both files. */
    fun seal(root: Path) {
        chmod(root, "rwx------")
        chmod(root.resolve("ca"), "rwx------")
        chmod(root.resolve("ca/ca.crt"), "rw-------")
        chmod(root.resolve("ca/ca.key"), "rw-------")
    }

    fun chmod(
        path: Path,
        mode: String,
    ) {
        Files.setPosixFilePermissions(path, PosixFilePermissions.fromString(mode))
    }

    /** Every line of the key's base64 body and its PEM text: what must never reach a log or an error. */
    fun keyFragments(keyPem: String): List<String> = keyPem.lines().filter { it.isNotBlank() && !it.startsWith("-----") }
}
