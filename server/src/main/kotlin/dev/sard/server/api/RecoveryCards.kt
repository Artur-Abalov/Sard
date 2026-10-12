// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import dev.sard.server.install.isReleaseTag

private const val PROCEDURE =
    "https://github.com/Artur-Abalov/Sard/blob/%s/docs/operator/06-data-and-backup.md" +
        "#восстановление-из-самобэкапа-f7"
private const val MAX_LOCATION = 1024

/** What follows the scheme of an address, up to the first slash: user[:password]@host and the like. */
private val AUTHORITY = Regex("^(?:s3:|sftp:)?(?://)?(?:https?://)?([^/]*)")

/**
 * Where the restore procedure is described for [version] of the server (Р28): the document of its tag, or of the
 * main branch for a version that is no release (the tag does not exist then).
 */
fun recoveryProcedureUrl(version: String): String {
    val ref = if (isReleaseTag("v$version")) "v$version" else "main"
    return PROCEDURE.format(ref)
}

private fun hasPassword(location: String): Boolean {
    val authority =
        AUTHORITY
            .find(location)
            ?.groupValues
            ?.get(1)
            .orEmpty()
    val userinfo = authority.substringBeforeLast('@', missingDelimiterValue = "")
    return ':' in userinfo
}

/** The address holds no password in a user:password@ and no control character, and is not long (OQ-203). */
fun isSafeLocation(location: String): Boolean =
    location.isNotEmpty() &&
        location.length <= MAX_LOCATION &&
        location.none { it.isISOControl() } &&
        !hasPassword(location)
