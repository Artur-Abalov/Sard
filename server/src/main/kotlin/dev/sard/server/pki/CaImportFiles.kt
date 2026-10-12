// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.pki

import java.io.IOException
import java.nio.file.Files
import java.nio.file.Path

/**
 * The first checks of a source of a CA, in the order of the table of refusals (ADR 0052): the directory is there,
 * the two files are there, the server can read them and nobody but their owner can. Nothing is read from them here
 * but their bytes, and no refusal quotes those.
 */
internal class CaImportFiles(
    private val root: Path,
    private val certPath: Path,
    private val keyPath: Path,
    private val isReadable: (Path) -> Boolean,
) {
    private val paths = listOf(root, certPath.parent, certPath, keyPath)

    fun requireUsable() {
        requireDirectory()
        requireFile(certPath)
        requireFile(keyPath)
        requireReadable()
        requireOwnerOnly()
    }

    fun text(path: Path): String =
        try {
            String(Files.readAllBytes(path), Charsets.ISO_8859_1)
        } catch (_: IOException) {
            throw refusal(CaImportRefusal.IMPORT_FILE_UNREADABLE, "$path cannot be read by the server")
        }

    private fun requireDirectory() {
        if (!Files.isDirectory(root)) {
            throw refusal(CaImportRefusal.IMPORT_SOURCE_MISSING, "$root does not exist or is not a directory")
        }
    }

    private fun requireReadable() {
        val unreadable = paths.firstOrNull { !isReadable(it) } ?: return
        throw refusal(CaImportRefusal.IMPORT_FILE_UNREADABLE, "$unreadable cannot be read by the server")
    }

    private fun requireOwnerOnly() {
        for (path in paths) {
            ownerOnlyViolation(path)?.let { throw refusal(CaImportRefusal.IMPORT_PERMISSIONS_TOO_OPEN, it) }
        }
    }

    private fun requireFile(path: Path) {
        if (!Files.isRegularFile(path)) throw refusal(CaImportRefusal.IMPORT_FILE_MISSING, "expected $path")
    }

    private fun refusal(
        reason: CaImportRefusal,
        detail: String,
    ): CaImportRefused = CaImportRefused(reason, detail, root)
}
