// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.pki

/** Why the CA directory and the database do not agree (F4a, Р19). */
enum class CaStartRefusal {
    /** There is a CA in the CA directory, and the database does not know where it came from. */
    CA_ORIGIN_NOT_RECORDED,

    /** The CA directory is empty, no source brings the CA back, and the database has a CA in use. */
    CA_MISSING,
}

/**
 * The server does not start: its CA directory and its database are not one installation. The message starts with
 * "CA startup refused", names the reason, the path and the fingerprint; it never quotes key material.
 */
class CaStartRefused(
    val reason: CaStartRefusal,
    detail: String,
) : IllegalStateException("CA startup refused: $reason: $detail")
