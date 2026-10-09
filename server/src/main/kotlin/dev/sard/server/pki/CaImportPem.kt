// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.pki

import java.util.Base64

private const val BEGIN = "-----BEGIN"

/** The DER of the one PEM block of [type] in [text]; empty unless there is exactly one block and nothing else. */
internal fun singlePemBlock(
    type: String,
    text: String,
): ByteArray {
    val pattern = Regex("$BEGIN $type-----(.*?)-----END $type-----", RegexOption.DOT_MATCHES_ALL)
    val body =
        pattern
            .findAll(text)
            .singleOrNull()
            ?.groupValues
            ?.get(1)
    val alone = text.split(BEGIN).size == 2
    return decode(body.takeIf { alone })
}

private fun decode(body: String?): ByteArray {
    val decoded = body?.let { runCatching { Base64.getMimeDecoder().decode(it) }.getOrNull() }
    return decoded ?: ByteArray(0)
}
