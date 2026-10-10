// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.pki

/**
 * Told when a start swapped the server's CA for an imported one (F4a, Р11): whatever refers to the CA by its
 * fingerprint, such as the enrollment tokens that carry it, must let go of [previous].
 */
fun interface CaReplacementListener {
    fun replaced(
        previous: CaFingerprint,
        current: CaFingerprint,
    )
}
