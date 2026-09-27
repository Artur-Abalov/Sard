// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.pki

/** [transform] over every element, or null as soon as one of them does. */
internal inline fun <T> List<T>.mapOrNull(transform: (T) -> Int?): List<Int>? {
    val result = mutableListOf<Int>()
    for (item in this) result += transform(item) ?: return null
    return result
}

/** [combine] applied to [a] and [b] if both are non-null, else null. */
internal inline fun <A, B, R> both(
    a: A?,
    b: B?,
    combine: (A, B) -> R,
): R? = a?.let { av -> b?.let { bv -> combine(av, bv) } }
