// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.auth

import org.springframework.beans.factory.annotation.Value
import org.springframework.stereotype.Component
import java.nio.charset.StandardCharsets
import java.security.MessageDigest

private const val MIN_PASSWORD_LENGTH = 12
private const val DIGEST_ALGORITHM = "SHA-256"

/** SHA-256 of [value]; always [MessageDigest.getDigestLength] bytes regardless of input length. */
fun sha256(value: String): ByteArray {
    val bytes = value.toByteArray(StandardCharsets.UTF_8)
    return MessageDigest.getInstance(DIGEST_ALGORITHM).digest(bytes)
}

/**
 * The single administrator password (D2). Only its SHA-256 hash is kept; the raw value
 * passed to the constructor is never stored in a field. [matches] compares two digests
 * of the same fixed length with [MessageDigest.isEqual], which the JDK documents as
 * comparing every byte without an early exit (no timing side channel).
 */
@Component
class AdminPasswordAuthenticator(
    @Value("\${SARD_ADMIN_PASSWORD:}") rawPassword: String,
) {
    private val hash: ByteArray = validateAndHash(rawPassword)

    fun matches(candidate: String): Boolean = MessageDigest.isEqual(sha256(candidate), hash)

    private fun validateAndHash(raw: String): ByteArray {
        require(raw.isNotEmpty()) { "SARD_ADMIN_PASSWORD is not set" }
        val codePoints = raw.codePointCount(0, raw.length)
        require(codePoints >= MIN_PASSWORD_LENGTH) {
            "SARD_ADMIN_PASSWORD must be at least $MIN_PASSWORD_LENGTH characters"
        }
        return sha256(raw)
    }
}
