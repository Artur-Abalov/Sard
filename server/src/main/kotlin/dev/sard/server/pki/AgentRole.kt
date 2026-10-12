// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.pki

import java.security.cert.X509Certificate

/**
 * What an agent may do, fixed when its enrollment token is issued and never changed (V1): a source
 * runs BACKUP, RESTORE and RUN; a verifier runs only VERIFY. [stored] is the form in the database,
 * in the certificate and in REST.
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
    /** No role URI in the SAN. */
    data object Missing : CertificateRole

    /** Several role URIs, or one that is not exactly "sard://roles/source" or "sard://roles/verifier". */
    data object Invalid : CertificateRole

    data class Valid(
        val role: AgentRole,
    ) : CertificateRole
}

private const val SAN_URI = 6

/**
 * The role of an agent certificate: a second URI in the subjectAltName next to the identity URI,
 * exactly "sard://roles/source" or "sard://roles/verifier". A role URI is any SAN URI with scheme
 * sard and host roles (both case-insensitive, RFC 3986); the agent holds the same rule.
 */
object AgentRoleUri {
    private const val SCHEME = "sard"
    private const val HOST = "roles"
    private val PREFIX = "$SCHEME://$HOST/"

    /** The role URI the CA writes for [role]. */
    fun of(role: AgentRole): String = PREFIX + role.stored

    fun read(certificate: X509Certificate): CertificateRole {
        val roleUris =
            certificate.subjectAlternativeNames
                .orEmpty()
                .filter { it[0] == SAN_URI }
                .map { it[1] as String }
                .filter(::isRoleUri)
        return when (roleUris.size) {
            0 -> CertificateRole.Missing
            1 -> exact(roleUris.single())
            else -> CertificateRole.Invalid
        }
    }

    private fun exact(uri: String): CertificateRole {
        val role = if (uri.startsWith(PREFIX)) AgentRole.parse(uri.removePrefix(PREFIX)) else null
        return if (role == null) CertificateRole.Invalid else CertificateRole.Valid(role)
    }

    private fun isRoleUri(uri: String): Boolean {
        val scheme = uri.substringBefore("://", missingDelimiterValue = "")
        val authority = uri.substringAfter("://", missingDelimiterValue = "").takeWhile { it !in "/?#" }
        val host = authority.substringAfterLast('@').substringBefore(':')
        return scheme.equals(SCHEME, ignoreCase = true) && host.equals(HOST, ignoreCase = true)
    }
}
