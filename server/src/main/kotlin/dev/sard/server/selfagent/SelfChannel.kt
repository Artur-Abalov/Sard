// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.selfagent

import java.io.IOException
import java.nio.file.Files
import java.nio.file.NoSuchFileException
import java.nio.file.Path
import java.nio.file.StandardCopyOption.ATOMIC_MOVE
import java.nio.file.StandardCopyOption.REPLACE_EXISTING
import java.nio.file.attribute.PosixFilePermission.OWNER_READ
import java.nio.file.attribute.PosixFilePermission.OWNER_WRITE
import java.nio.file.attribute.PosixFilePermissions

private const val DOCS = "docs/operations/self-agent.md"
private const val TOKEN_FILE = "enroll-token"
private const val STAGING = ".tmp-"
private val OWNER_FILE = PosixFilePermissions.asFileAttribute(setOf(OWNER_READ, OWNER_WRITE))

/**
 * The directory shared with the agent next to the server (SARD_SELF_DIR): `enroll-token`, written whole through a
 * staging file in the same directory and a rename, so a reader sees the old content or the new one, never half.
 * The password of the agent's database role is not here: db-init puts it into a volume of its own (DB2), and the
 * server neither writes nor reads it. Throws [IllegalStateException] naming [dir] when the server cannot write there.
 */
class SelfChannel(
    private val dir: Path,
) {
    val tokenFile: Path = dir.resolve(TOKEN_FILE)

    init {
        check(Files.isDirectory(dir)) { unusable("is not a directory") }
        check(writable()) { unusable("is not writable by the server") }
    }

    /** The token string in the file, or null when there is no file. */
    fun readToken(): String? = read(tokenFile)

    fun writeToken(token: String) = write(tokenFile, token)

    fun deleteToken() {
        Files.deleteIfExists(tokenFile)
    }

    private fun unusable(why: String) = "SARD_SELF_DIR=$dir $why; see $DOCS"

    private fun writable(): Boolean =
        try {
            Files.delete(Files.createTempFile(dir, STAGING, "", OWNER_FILE))
            true
        } catch (_: IOException) {
            false
        }

    private fun read(file: Path): String? =
        try {
            Files.readString(file)
        } catch (_: NoSuchFileException) {
            null
        }

    private fun write(
        file: Path,
        text: String,
    ) {
        val staging = Files.createTempFile(dir, STAGING, "", OWNER_FILE)
        try {
            Files.writeString(staging, text)
            Files.move(staging, file, ATOMIC_MOVE, REPLACE_EXISTING)
        } finally {
            Files.deleteIfExists(staging)
        }
    }
}
