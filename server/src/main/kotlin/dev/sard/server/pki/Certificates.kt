// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.pki

import org.bouncycastle.asn1.x500.X500Name
import org.bouncycastle.asn1.x509.BasicConstraints
import org.bouncycastle.asn1.x509.ExtendedKeyUsage
import org.bouncycastle.asn1.x509.Extension
import org.bouncycastle.asn1.x509.GeneralName
import org.bouncycastle.asn1.x509.GeneralNames
import org.bouncycastle.asn1.x509.KeyPurposeId
import org.bouncycastle.asn1.x509.KeyUsage
import org.bouncycastle.asn1.x509.SubjectPublicKeyInfo
import org.bouncycastle.cert.X509v3CertificateBuilder
import org.bouncycastle.cert.jcajce.JcaX509CertificateConverter
import org.bouncycastle.cert.jcajce.JcaX509ExtensionUtils
import org.bouncycastle.operator.jcajce.JcaContentSignerBuilder
import java.math.BigInteger
import java.security.KeyPair
import java.security.PrivateKey
import java.security.SecureRandom
import java.security.cert.X509Certificate
import java.time.Duration
import java.time.Instant
import java.util.Date

private const val SERIAL_BITS = 128
private val CA_VALIDITY = Duration.ofDays(3650)
private val AGENT_VALIDITY = Duration.ofDays(365)
private val SERVER_VALIDITY = Duration.ofDays(90)

/** Tolerates clocks behind the server's when checking the root and the server certificate. */
private val BACKDATE = Duration.ofHours(1)
private val IPV4 = Regex("""\d{1,3}(\.\d{1,3}){3}""")

/** X.509 profiles of the file CA (ADR 0014). */
internal object Certificates {
    private val rootName = X500Name("CN=Sard CA")
    private val extensions = JcaX509ExtensionUtils()

    fun root(
        keys: KeyPair,
        now: Instant,
        random: SecureRandom,
    ): X509Certificate {
        val spki = SubjectPublicKeyInfo.getInstance(keys.public.encoded)
        val notBefore = date(now - BACKDATE)
        val notAfter = date(now + CA_VALIDITY)
        val builder =
            X509v3CertificateBuilder(rootName, serial(random), notBefore, notAfter, rootName, spki)
                .addExtension(Extension.basicConstraints, true, BasicConstraints(true))
                .addExtension(Extension.keyUsage, true, KeyUsage(KeyUsage.keyCertSign or KeyUsage.cRLSign))
                .addExtension(Extension.subjectKeyIdentifier, false, extensions.createSubjectKeyIdentifier(spki))
        return sign(builder, keys.private, random)
    }

    fun agent(
        key: SubjectPublicKeyInfo,
        agent: AgentIdentity,
        ca: CaKeyPair,
        now: Instant,
        random: SecureRandom,
    ): X509Certificate {
        val san = GeneralNames(GeneralName(GeneralName.uniformResourceIdentifier, agent.uri()))
        val profile = Leaf("CN=${agent.agentId}", KeyPurposeId.id_kp_clientAuth, san, now, now + AGENT_VALIDITY)
        return leaf(key, profile, ca, random)
    }

    fun server(
        key: SubjectPublicKeyInfo,
        names: List<String>,
        ca: CaKeyPair,
        now: Instant,
        random: SecureRandom,
    ): X509Certificate {
        val san = GeneralNames(names.map(::generalName).toTypedArray())
        val profile =
            Leaf("CN=${names.first()}", KeyPurposeId.id_kp_serverAuth, san, now - BACKDATE, now + SERVER_VALIDITY)
        return leaf(key, profile, ca, random)
    }

    private class Leaf(
        val subject: String,
        val purpose: KeyPurposeId,
        val san: GeneralNames,
        val notBefore: Instant,
        val notAfter: Instant,
    )

    private fun leaf(
        key: SubjectPublicKeyInfo,
        profile: Leaf,
        ca: CaKeyPair,
        random: SecureRandom,
    ): X509Certificate {
        val issuer = X500Name.getInstance(ca.certificate.subjectX500Principal.encoded)
        val builder =
            X509v3CertificateBuilder(
                issuer,
                serial(random),
                date(profile.notBefore),
                date(profile.notAfter),
                X500Name(profile.subject),
                key,
            ).addExtension(Extension.basicConstraints, true, BasicConstraints(false))
                .addExtension(Extension.keyUsage, true, KeyUsage(KeyUsage.digitalSignature))
                .addExtension(Extension.extendedKeyUsage, false, ExtendedKeyUsage(profile.purpose))
                .addExtension(Extension.subjectAlternativeName, false, profile.san)
                .addExtension(Extension.subjectKeyIdentifier, false, extensions.createSubjectKeyIdentifier(key))
                .addExtension(Extension.authorityKeyIdentifier, false, authorityKeyIdentifier(ca))
        return sign(builder, ca.privateKey, random)
    }

    private fun authorityKeyIdentifier(ca: CaKeyPair) = extensions.createAuthorityKeyIdentifier(ca.certificate)

    /** Random, positive, exactly [SERIAL_BITS] long (RFC 5280 allows up to 20 octets). */
    private fun serial(random: SecureRandom): BigInteger = BigInteger(SERIAL_BITS, random).setBit(SERIAL_BITS - 1)

    private fun generalName(name: String): GeneralName {
        val ip = ':' in name || IPV4.matches(name)
        return GeneralName(if (ip) GeneralName.iPAddress else GeneralName.dNSName, name)
    }

    private fun sign(
        builder: X509v3CertificateBuilder,
        key: PrivateKey,
        random: SecureRandom,
    ): X509Certificate {
        val signer = JcaContentSignerBuilder(Keys.SIGNATURE).setSecureRandom(random).build(key)
        return JcaX509CertificateConverter().getCertificate(builder.build(signer))
    }

    private fun date(instant: Instant) = Date.from(instant)
}
