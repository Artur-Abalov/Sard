// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.e2e

/**
 * Masks secrets before container logs reach the artifacts: enrollment tokens
 * (docs/specs/enrollment-token.md), PEM private keys and every value a test
 * registered as secret (database password, issued keys).
 */
internal object Redaction {
    private val token = Regex("""sard_[A-Za-z0-9_-]{43}\.[0-9a-f]{64}""")
    private val privateKey =
        Regex("""-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?-----END [A-Z ]*PRIVATE KEY-----""")

    fun apply(
        text: String,
        secrets: Collection<String>,
    ): String {
        val masked =
            secrets
                .filter { it.isNotEmpty() }
                .sortedByDescending { it.length }
                .fold(text) { acc, secret -> acc.replace(secret, "[redacted]") }
        return privateKey
            .replace(token.replace(masked, "sard_[redacted]"), "[redacted private key]")
    }
}
