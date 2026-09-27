// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.enrollment

import dev.sard.server.enrollment.MalformedEnrollmentTokenException.Reason
import dev.sard.server.pki.CaFingerprint
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import java.security.SecureRandom
import java.util.HexFormat
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertFalse
import kotlin.test.assertNotEquals

/** The test vector of docs/specs/enrollment-token.md; A2a checks the same values on the agent side. */
private val VECTOR_SECRET = ByteArray(32) { it.toByte() }
private val VECTOR_FINGERPRINT = CaFingerprint("8544e2352a80a3d403eed68f8bb4ff271d0ce02c09c1d0faf6f090cea423be9f")
private const val VECTOR_TOKEN =
    "sard_AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8.8544e2352a80a3d403eed68f8bb4ff271d0ce02c09c1d0faf6f090cea423be9f"
private const val VECTOR_HASH = "630dcd2966c4336691125448bbb25b4ff412a49c732db2c8abc1b8581bd710dd"

private const val SECRET = "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8"
private const val FINGERPRINT = "8544e2352a80a3d403eed68f8bb4ff271d0ce02c09c1d0faf6f090cea423be9f"

private fun hex(bytes: ByteArray) = HexFormat.of().formatHex(bytes)

@MutFlowTest
class EnrollmentTokenTest {
    @Test
    fun `the test vector encodes to the documented string`() {
        val token = EnrollmentToken(EnrollmentSecret(VECTOR_SECRET), VECTOR_FINGERPRINT)
        assertEquals(VECTOR_TOKEN, MutFlow.underTest { token.encode() })
    }

    @Test
    fun `the stored hash is SHA-256 of the 32 secret bytes`() {
        val secret = EnrollmentSecret(VECTOR_SECRET)
        assertEquals(VECTOR_HASH, hex(MutFlow.underTest { secret.hash() }))
    }

    @Test
    fun `parsing the test vector returns its parts`() {
        val token = MutFlow.underTest { EnrollmentToken.parse(VECTOR_TOKEN) }
        assertEquals(VECTOR_FINGERPRINT, token.fingerprint)
        assertEquals(VECTOR_HASH, hex(token.secret.hash()))
        assertEquals(VECTOR_TOKEN, token.encode())
    }

    @Test
    fun `a random secret has 32 bytes and round-trips`() {
        val secret = EnrollmentSecret.random(SecureRandom())
        val encoded = EnrollmentToken(secret, VECTOR_FINGERPRINT).encode()
        val parsed = MutFlow.underTest { EnrollmentToken.parse(encoded) }
        assertEquals(hex(secret.hash()), hex(parsed.secret.hash()))
        assertEquals(1 + 4 + 43 + 1 + 64, encoded.length)
    }

    @Test
    fun `a secret must be exactly 32 bytes`() {
        assertFailsWith<IllegalArgumentException> { MutFlow.underTest { EnrollmentSecret(ByteArray(31)) } }
        assertFailsWith<IllegalArgumentException> { MutFlow.underTest { EnrollmentSecret(ByteArray(33)) } }
    }

    @Test
    fun `the secret does not change when the caller reuses its array`() {
        val bytes = VECTOR_SECRET.copyOf()
        val secret = MutFlow.underTest { EnrollmentSecret(bytes) }
        bytes.fill(0)
        assertEquals(VECTOR_HASH, hex(secret.hash()))
    }

    @Test
    fun `neither toString reveals the secret`() {
        val secret = EnrollmentSecret(VECTOR_SECRET)
        val token = EnrollmentToken(secret, VECTOR_FINGERPRINT)
        val shown = MutFlow.underTest { secret.toString() + token.toString() }
        assertFalse(SECRET in shown, shown)
        assertFalse(VECTOR_HASH in shown, shown)
        assertEquals("EnrollmentToken(fingerprint=$FINGERPRINT)", token.toString())
    }

    @Test
    fun `malformed strings fail with a typed reason`() {
        val cases =
            listOf(
                "" to Reason.PREFIX,
                "SARD_$SECRET.$FINGERPRINT" to Reason.PREFIX,
                "sard$SECRET.$FINGERPRINT" to Reason.PREFIX,
                "sard_$SECRET$FINGERPRINT" to Reason.SEPARATOR,
                "sard_$SECRET.$FINGERPRINT.x" to Reason.SEPARATOR,
                "sard_$SECRET..$FINGERPRINT" to Reason.SEPARATOR,
                "sard_${SECRET.drop(1)}.$FINGERPRINT" to Reason.SECRET_LENGTH,
                "sard_${SECRET}A.$FINGERPRINT" to Reason.SECRET_LENGTH,
                "sard_$SECRET=.$FINGERPRINT" to Reason.SECRET_LENGTH,
                "sard_${SECRET.dropLast(1)}+.$FINGERPRINT" to Reason.SECRET_ALPHABET,
                "sard_${SECRET.dropLast(1)}/.$FINGERPRINT" to Reason.SECRET_ALPHABET,
                "sard_${SECRET.dropLast(1)}9.$FINGERPRINT" to Reason.SECRET_ENCODING,
                "sard_$SECRET.${FINGERPRINT.drop(1)}" to Reason.FINGERPRINT_LENGTH,
                "sard_$SECRET.${FINGERPRINT}0" to Reason.FINGERPRINT_LENGTH,
                "sard_$SECRET.${FINGERPRINT.uppercase()}" to Reason.FINGERPRINT_ALPHABET,
                "sard_$SECRET.${FINGERPRINT.dropLast(1)}g" to Reason.FINGERPRINT_ALPHABET,
            )
        for ((text, reason) in cases) {
            val error = assertFailsWith<MalformedEnrollmentTokenException>(text) { parse(text) }
            assertEquals(reason, error.reason, text)
        }
    }

    @Test
    fun `a parse error does not echo the token`() {
        val text = "sard_$SECRET.${FINGERPRINT.uppercase()}"
        val error = assertFailsWith<MalformedEnrollmentTokenException> { parse(text) }
        val shown = error.toString()
        assertFalse(SECRET in shown, shown)
        assertNotEquals(null, error.message)
    }

    private fun parse(text: String) = MutFlow.underTest { EnrollmentToken.parse(text) }
}
