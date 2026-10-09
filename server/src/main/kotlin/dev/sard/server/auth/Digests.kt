// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.auth

import java.nio.charset.StandardCharsets
import java.security.MessageDigest

private const val DIGEST_ALGORITHM = "SHA-256"

/** SHA-256 of [value]; always [MessageDigest.getDigestLength] bytes regardless of input length. */
fun sha256(value: String): ByteArray {
    val bytes = value.toByteArray(StandardCharsets.UTF_8)
    return MessageDigest.getInstance(DIGEST_ALGORITHM).digest(bytes)
}
