// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.enrollment

import dev.sard.server.pki.CaFingerprint
import java.security.MessageDigest
import java.security.SecureRandom
import java.util.Base64

private const val TOKEN_PREFIX = "sard_"
private const val TOKEN_SEPARATOR = '.'
private const val SECRET_BYTES = 32
private const val SECRET_CHARS = 43
private const val FINGERPRINT_CHARS = 64
private val SECRET_ALPHABET = Regex("[A-Za-z0-9_-]+")
private val FINGERPRINT_ALPHABET = Regex("[0-9a-f]+")

/**
 * The 32 random bytes of an enrollment token. Only [hash] is ever stored; the bytes
 * never appear in [toString], logs or exception messages.
 */
class EnrollmentSecret(
    bytes: ByteArray,
) {
    private val bytes = bytes.copyOf()

    init {
        require(bytes.size == SECRET_BYTES) { "an enrollment secret has $SECRET_BYTES bytes" }
    }

    /** SHA-256 of the raw bytes: the `enrollment_tokens.token_hash` column. */
    fun hash(): ByteArray = MessageDigest.getInstance("SHA-256").digest(bytes)

    internal fun encoded(): String = Base64.getUrlEncoder().withoutPadding().encodeToString(bytes)

    override fun toString() = "EnrollmentSecret(redacted)"

    companion object {
        fun random(random: SecureRandom) = EnrollmentSecret(ByteArray(SECRET_BYTES).also(random::nextBytes))
    }
}

/**
 * `sard_<secret>.<fingerprint>` (docs/specs/enrollment-token.md): the secret in
 * base64url without padding, the CA fingerprint the agent pins on first contact.
 */
class EnrollmentToken(
    val secret: EnrollmentSecret,
    val fingerprint: CaFingerprint,
) {
    /** The string handed to the operator once; the server keeps only [EnrollmentSecret.hash]. */
    fun encode(): String = TOKEN_PREFIX + secret.encoded() + TOKEN_SEPARATOR + fingerprint.hex

    override fun toString() = "EnrollmentToken(fingerprint=${fingerprint.hex})"

    companion object {
        /** Strict parse; throws [MalformedEnrollmentTokenException] naming the first rule broken. */
        fun parse(text: String): EnrollmentToken {
            reject(!text.startsWith(TOKEN_PREFIX), MalformedEnrollmentTokenException.Reason.PREFIX)
            val parts = text.substring(TOKEN_PREFIX.length).split(TOKEN_SEPARATOR)
            reject(parts.size != 2, MalformedEnrollmentTokenException.Reason.SEPARATOR)
            return EnrollmentToken(parseSecret(parts[0]), parseFingerprint(parts[1]))
        }

        private fun parseSecret(text: String): EnrollmentSecret {
            reject(text.length != SECRET_CHARS, MalformedEnrollmentTokenException.Reason.SECRET_LENGTH)
            reject(!SECRET_ALPHABET.matches(text), MalformedEnrollmentTokenException.Reason.SECRET_ALPHABET)
            val secret = EnrollmentSecret(Base64.getUrlDecoder().decode(text))
            // 43 characters carry 258 bits; the last two must be zero, or two strings name one secret.
            reject(secret.encoded() != text, MalformedEnrollmentTokenException.Reason.SECRET_ENCODING)
            return secret
        }

        private fun parseFingerprint(text: String): CaFingerprint {
            reject(text.length != FINGERPRINT_CHARS, MalformedEnrollmentTokenException.Reason.FINGERPRINT_LENGTH)
            reject(!FINGERPRINT_ALPHABET.matches(text), MalformedEnrollmentTokenException.Reason.FINGERPRINT_ALPHABET)
            return CaFingerprint(text)
        }

        private fun reject(
            broken: Boolean,
            reason: MalformedEnrollmentTokenException.Reason,
        ) {
            if (broken) throw MalformedEnrollmentTokenException(reason)
        }
    }
}

/** The string is not an enrollment token; the message names the rule, never the input. */
class MalformedEnrollmentTokenException(
    val reason: Reason,
) : IllegalArgumentException("malformed enrollment token: ${reason.description}") {
    enum class Reason(
        val description: String,
    ) {
        PREFIX("does not start with $TOKEN_PREFIX"),
        SEPARATOR("needs exactly one '$TOKEN_SEPARATOR'"),
        SECRET_LENGTH("the secret is not $SECRET_CHARS characters"),
        SECRET_ALPHABET("the secret is not unpadded base64url"),
        SECRET_ENCODING("the secret is not canonical base64url"),
        FINGERPRINT_LENGTH("the fingerprint is not $FINGERPRINT_CHARS characters"),
        FINGERPRINT_ALPHABET("the fingerprint is not lower-case hex"),
    }
}
