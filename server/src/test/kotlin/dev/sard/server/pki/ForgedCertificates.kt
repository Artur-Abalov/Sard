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

/** What the SAN of a forged certificate says about the agent's role besides its identity URI (tests only). */
sealed interface ForgedRole {
    /** No role URI at all. */
    data object Absent : ForgedRole

    /** The single role URI "sard://roles/[text]". */
    data class Text(
        val text: String,
    ) : ForgedRole

    /** These URIs, verbatim, after the identity URI or (with [first]) before it. */
    data class Uris(
        val uris: List<String>,
        val first: Boolean = false,
    ) : ForgedRole
}

/** Single SAN URIs with scheme sard and host roles that are not exactly a role: all "invalid" (tests only). */
val INEXACT_ROLE_URIS =
    listOf(
        "sard://roles/admin",
        "sard://roles/Source",
        "SARD://ROLES/source",
        "sard://roles/",
        "sard://roles",
        "sard://roles/source/",
        "sard://roles/source?x=1",
        "sard://roles/source#x",
        "sard://u@roles/source",
        "sard://roles:443/source",
        "sard://Roles",
    )

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
                    GeneralNames(names(identity, role)),
                )
        val signer = JcaContentSignerBuilder(Keys.SIGNATURE).setSecureRandom(SecureRandom()).build(ca.privateKey)
        return JcaX509CertificateConverter().getCertificate(builder.build(signer))
    }

    private fun names(
        identity: AgentIdentity,
        role: ForgedRole,
    ): Array<GeneralName> {
        val own = listOf(identity.uri())
        val uris =
            when (role) {
                ForgedRole.Absent -> own
                is ForgedRole.Text -> own + "sard://roles/${role.text}"
                is ForgedRole.Uris -> if (role.first) role.uris + own else own + role.uris
            }
        return uris.map { GeneralName(GeneralName.uniformResourceIdentifier, it) }.toTypedArray()
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
