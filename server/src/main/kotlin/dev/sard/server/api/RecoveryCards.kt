// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import dev.sard.server.install.isReleaseTag

private const val PROCEDURE =
    "https://github.com/Artur-Abalov/Sard/blob/%s/docs/operator/06-data-and-backup.md" +
        "#восстановление-из-самобэкапа-f7"

/**
 * Where the restore procedure is described for [version] of the server (Р28): the document of its tag, or of the
 * main branch for a version that is no release (the tag does not exist then).
 */
fun recoveryProcedureUrl(version: String): String {
    val ref = if (isReleaseTag("v$version")) "v$version" else "main"
    return PROCEDURE.format(ref)
}
