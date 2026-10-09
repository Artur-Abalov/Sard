// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.onboarding

import dev.sard.server.api.SetupCodeState
import dev.sard.server.auth.sha256
import java.security.MessageDigest
import java.security.SecureRandom
import java.time.Clock
import java.time.Duration
import java.time.Instant

/** The Crockford alphabet without I, L, O and U: 32 symbols, 5 bits each. */
private const val ALPHABET = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
private const val CODE_LENGTH = 28
private const val GROUP = 4
private val VALIDITY: Duration = Duration.ofHours(24)

/** Makes the printed form of a code, `XXXX-XXXX-...`; tests substitute it (Р1). */
fun interface SetupCodeGenerator {
    fun generate(): String
}

/** 28 symbols of the alphabet from a cryptographic generator: 140 bits, in 7 groups of 4. */
class RandomSetupCodeGenerator(
    private val random: SecureRandom = SecureRandom(),
) : SetupCodeGenerator {
    override fun generate(): String =
        (1..CODE_LENGTH)
            .map { ALPHABET[random.nextInt(ALPHABET.length)] }
            .chunked(GROUP)
            .joinToString("-") { it.joinToString("") }
}

/** A code just issued: the only moment its text exists in the server. */
class IssuedCode(
    val display: String,
    val expiresAt: Instant,
)

/**
 * The setup code of this process (D15, Р1-Р4): issued once at start while the admin step is open, valid for 24 hours,
 * gone when the step is done. Only the SHA-256 of the normalised code is kept; the text is never stored, and the
 * comparison covers two digests of the same length without an early exit.
 */
class SetupCodes(
    private val clock: Clock,
    private val generator: SetupCodeGenerator,
) {
    private class Held(
        val hash: ByteArray,
        val expiresAt: Instant,
    )

    @Volatile
    private var held: Held? = null

    fun issue(): IssuedCode {
        val display = generator.generate()
        val expiresAt = clock.instant() + VALIDITY
        held = Held(sha256(checkNotNull(normalize(display))), expiresAt)
        return IssuedCode(display, expiresAt)
    }

    fun state(): SetupCodeState {
        val current = held ?: return SetupCodeState.NOT_ISSUED
        return if (clock.instant().isBefore(current.expiresAt)) SetupCodeState.ACTIVE else SetupCodeState.EXPIRED
    }

    fun expiresAt(): Instant? = held?.expiresAt

    /** True for a code that was issued, has not expired and is written in any of the accepted ways. */
    fun accepts(input: String?): Boolean {
        val current = held
        val candidate = sha256(input?.let(::normalize).orEmpty())
        val same = MessageDigest.isEqual(candidate, current?.hash ?: ByteArray(candidate.size))
        return same and (state() == SetupCodeState.ACTIVE)
    }

    /** The admin step is done: the code is no more. */
    fun close() {
        held = null
    }

    companion object {
        /** Case, hyphens and white space do not matter; I and L read as 1, O as 0 (OQ-194). Null unless 28 symbols. */
        fun normalize(input: String): String? {
            val symbols =
                input
                    .filterNot { it == '-' || it.isWhitespace() }
                    .uppercase()
                    .map { reading(it) }
            return symbols.joinToString("").takeIf { it.length == CODE_LENGTH && it.all { c -> c in ALPHABET } }
        }

        private fun reading(symbol: Char): Char =
            when (symbol) {
                'I', 'L' -> '1'
                'O' -> '0'
                else -> symbol
            }
    }
}
