// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.auth

private const val MIN_CODE_POINTS = 12
private const val MAX_CODE_POINTS = 1024

/** What a password to be set must be (Р7): 12 to 1024 Unicode code points, taken as typed. */
object PasswordRules {
    fun acceptable(password: String): Boolean = password.codePointCount(0, password.length) in MIN_CODE_POINTS..MAX_CODE_POINTS
}
