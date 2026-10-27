// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.runs

private const val SECONDS_PER_HOUR = 3600L

/** A step timeout of [seconds] in the biggest unit that holds it whole: "6 h", "90 min", "59 s". */
internal fun timeoutText(seconds: Long): String =
    when {
        seconds % SECONDS_PER_HOUR == 0L -> "${seconds / SECONDS_PER_HOUR} h"
        seconds % SECONDS_PER_MINUTE == 0L -> "${seconds / SECONDS_PER_MINUTE} min"
        else -> "$seconds s"
    }
