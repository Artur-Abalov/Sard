// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import java.util.Optional

/** The longest step timeout a source or the installation may set: a day (ВJ1). */
internal const val TIMEOUT_MAX_MINUTES = 1440

/** [minutes] of [field] as the domain takes it: null stays null (the installation's default), else 1 to a day. */
internal fun timeoutOf(
    field: String,
    minutes: Int?,
): Int? {
    if (minutes != null && minutes !in 1..TIMEOUT_MAX_MINUTES) {
        throw RequestInvalid(field, "must be 1 to $TIMEOUT_MAX_MINUTES minutes")
    }
    return minutes
}

/** A timeout field of a source request: [omitted] when the request did not carry it (ДJ13), else [minutes] or null. */
internal data class SourceTimeout(
    val minutes: Int?,
    val omitted: Boolean,
) {
    companion object {
        /** [sent] as Jackson reads it: null is an omitted field, empty an explicit null, present a number. */
        fun of(
            field: String,
            sent: Optional<Int>?,
        ) = SourceTimeout(timeoutOf(field, sent?.orElse(null)), sent == null)
    }
}
