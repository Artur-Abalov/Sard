// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.e2e

import java.util.HexFormat
import kotlin.test.Test
import kotlin.test.assertEquals

/** The token helper against the test vector of docs/specs/enrollment-token.md ("Тестовый вектор"). */
class EnrollmentTokenFormatTest {
    private val secret = ByteArray(32) { it.toByte() }
    private val fingerprint = "8544e2352a80a3d403eed68f8bb4ff271d0ce02c09c1d0faf6f090cea423be9f"

    @Test
    fun `the token string matches the specification's vector`() {
        assertEquals(
            "sard_AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8.$fingerprint",
            EnrollmentTokens.format(secret, fingerprint),
        )
    }

    @Test
    fun `the stored hash matches the specification's vector`() {
        assertEquals(
            "630dcd2966c4336691125448bbb25b4ff412a49c732db2c8abc1b8581bd710dd",
            HexFormat.of().formatHex(EnrollmentTokens.hash(secret)),
        )
    }

    @Test
    fun `the vector token is masked in collected logs`() {
        val token = EnrollmentTokens.format(secret, fingerprint)
        assertEquals("token sard_[redacted] used", Redaction.apply("token $token used", emptySet()))
    }
}
