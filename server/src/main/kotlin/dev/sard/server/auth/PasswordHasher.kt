// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.auth

import org.springframework.security.crypto.argon2.Argon2PasswordEncoder

private const val SALT_BYTES = 16
private const val HASH_BYTES = 32
private const val PARALLELISM = 1
private const val MEMORY_KIB = 19_456
private const val ITERATIONS = 2

/**
 * Argon2id for the administrator password (Р8, OQ-193): a PHC string with a 16-byte salt, a 32-byte hash,
 * m=19456 KiB, t=2, p=1. Nothing else of Spring Security is used.
 */
class PasswordHasher {
    private val encoder = Argon2PasswordEncoder(SALT_BYTES, HASH_BYTES, PARALLELISM, MEMORY_KIB, ITERATIONS)

    fun hash(password: String): String = checkNotNull(encoder.encode(password))

    /** False for a wrong password and for a stored value that is no Argon2 hash at all. */
    fun matches(
        password: String,
        hash: String,
    ): Boolean = runCatching { encoder.matches(password, hash) }.getOrDefault(false)
}
