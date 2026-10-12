// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.pki

import org.bouncycastle.asn1.ASN1ObjectIdentifier
import org.bouncycastle.asn1.ASN1OctetString
import org.bouncycastle.asn1.ASN1Primitive
import org.bouncycastle.asn1.ASN1UTF8String
import org.bouncycastle.asn1.DERUTF8String
import java.io.IOException
import java.security.cert.X509Certificate

/**
 * What an agent may do, fixed when its enrollment token is issued and never changed (V1): a source
 * runs BACKUP, RESTORE and RUN; a verifier runs only VERIFY. [stored] is the form in the database,
 * in the certificate extension and in REST.
 */
enum class AgentRole(
    val stored: String,
) {
    SOURCE("source"),
    VERIFIER("verifier"),
    ;

    companion object {
        /** The role stored as [stored]; fails for anything else (the database CHECK allows nothing else). */
        fun of(stored: String): AgentRole = checkNotNull(parse(stored)) { "unknown agent role '$stored'" }

        /** The role spelled exactly [text], or null. */
        fun parse(text: String): AgentRole? = entries.firstOrNull { it.stored == text }
    }
}

/** What a certificate says about its holder's role (S3 reads it on every call). */
sealed interface CertificateRole {
    /** No Sard role extension. */
    data object Missing : CertificateRole

    /** The extension is there but is no DER UTF8String "source" or "verifier". */
    data object Invalid : CertificateRole

    data class Valid(
        val role: AgentRole,
    ) : CertificateRole
}

/**
 * The Sard role extension of an agent certificate: a non-critical X.509 extension whose value is a
 * DER UTF8String, exactly "source" or "verifier". The OID is 2.25.<UUID> for the UUID
 * 378b97d4-9756-4764-8ea0-056286ce38b8 (docs/specs/server/agent-role.feature); the agent holds the same constant.
 */
object AgentRoleExtension {
    const val OID = "2.25.73832348518563859722830588044060276920"

    internal val IDENTIFIER = ASN1ObjectIdentifier(OID)

    internal fun value(role: AgentRole) = DERUTF8String(role.stored)

    fun read(certificate: X509Certificate): CertificateRole {
        val wrapped = certificate.getExtensionValue(OID) ?: return CertificateRole.Missing
        val role = decode(wrapped)?.let(AgentRole::parse)
        return if (role == null) CertificateRole.Invalid else CertificateRole.Valid(role)
    }

    /** The text of the DER UTF8String inside the extension's octet string, or null for anything else. */
    private fun decode(wrapped: ByteArray): String? =
        try {
            val inner = ASN1OctetString.getInstance(wrapped).octets
            ASN1UTF8String.getInstance(ASN1Primitive.fromByteArray(inner))?.string
        } catch (_: IOException) {
            null
        } catch (_: IllegalArgumentException) {
            null
        }
}
