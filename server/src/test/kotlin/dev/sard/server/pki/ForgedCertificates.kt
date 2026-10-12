// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.pki

import org.bouncycastle.asn1.ASN1Encodable
import org.bouncycastle.asn1.DERPrintableString
import org.bouncycastle.asn1.DERUTF8String
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
import org.bouncycastle.operator.jcajce.JcaContentSignerBuilder
import java.math.BigInteger
import java.nio.file.Files
import java.nio.file.Path
import java.security.KeyFactory
import java.security.KeyPair
import java.security.SecureRandom
import java.security.cert.X509Certificate
import java.security.spec.PKCS8EncodedKeySpec
import java.time.Duration
import java.time.Instant
import java.util.Date

/** What a forged certificate says about the agent's role (tests only). */
sealed interface ForgedRole {
    /** No Sard role extension at all. */
    data object Absent : ForgedRole

    /** The extension holds a DER UTF8String [text]. */
    data class Text(
        val text: String,
    ) : ForgedRole

    /** The extension holds a DER PrintableString [text]: right letters, wrong type. */
    data class Printable(
        val text: String,
    ) : ForgedRole

    /** The extension holds these bytes, whatever they are. */
    class Raw(
        val der: ByteArray,
    ) : ForgedRole
}

/**
 * Certificates signed by the real CA that no honest server would issue: the same serial and SAN
 * as a recorded one but another set of extensions (tests only, "поддельный сертификат").
 */
class ForgedCertificates(
    private val ca: CaKeyPair,
) {
    fun forge(
        agent: KeyPair,
        serial: BigInteger,
        identity: AgentIdentity,
        role: ForgedRole,
        now: Instant = Instant.now(),
    ): X509Certificate {
        val key = SubjectPublicKeyInfo.getInstance(agent.public.encoded)
        val issuer = X500Name.getInstance(ca.certificate.subjectX500Principal.encoded)
        val builder =
            X509v3CertificateBuilder(
                issuer,
                serial,
                Date.from(now - Duration.ofHours(1)),
                Date.from(now + Duration.ofDays(1)),
                X500Name("CN=${identity.agentId}"),
                key,
            ).addExtension(Extension.basicConstraints, true, BasicConstraints(false))
                .addExtension(Extension.keyUsage, true, KeyUsage(KeyUsage.digitalSignature))
                .addExtension(Extension.extendedKeyUsage, false, ExtendedKeyUsage(KeyPurposeId.id_kp_clientAuth))
                .addExtension(
                    Extension.subjectAlternativeName,
                    false,
                    GeneralNames(GeneralName(GeneralName.uniformResourceIdentifier, identity.uri())),
                )
        withRole(builder, role)
        val signer = JcaContentSignerBuilder(Keys.SIGNATURE).setSecureRandom(SecureRandom()).build(ca.privateKey)
        return JcaX509CertificateConverter().getCertificate(builder.build(signer))
    }

    private fun withRole(
        builder: X509v3CertificateBuilder,
        role: ForgedRole,
    ) {
        val oid = org.bouncycastle.asn1.ASN1ObjectIdentifier(AgentRoleExtension.OID)
        when (role) {
            ForgedRole.Absent -> Unit
            is ForgedRole.Text -> builder.addExtension(oid, false, DERUTF8String(role.text) as ASN1Encodable)
            is ForgedRole.Printable -> builder.addExtension(oid, false, DERPrintableString(role.text))
            is ForgedRole.Raw -> builder.addExtension(oid, false, role.der)
        }
    }

    companion object {
        /** The CA of a running [FileCertificateAuthority]: its certificate and the key file it keeps. */
        fun of(ca: CertificateAuthority): ForgedCertificates {
            val certificate = PkiFixtures.certificate(ca.caBundlePem())
            val pem = Files.readString(Path.of(ca.keyLocation()))
            val key = KeyFactory.getInstance("EC").generatePrivate(PKCS8EncodedKeySpec(Pem.decode("PRIVATE KEY", pem)))
            return ForgedCertificates(CaKeyPair(certificate, key))
        }
    }
}
