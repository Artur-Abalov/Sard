// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.e2e

import org.apache.commons.compress.archivers.tar.TarArchiveEntry
import org.apache.commons.compress.archivers.tar.TarArchiveOutputStream
import org.testcontainers.images.builder.Transferable

/**
 * Files and directories copied into a container with the owner and mode each must have.
 * `Transferable.of` cannot set the owner, so the tar entries are written here: the agent refuses
 * secret files owned by another user or open to others (A1), and a test needs a file the agent
 * cannot read.
 *
 * An entry's [Entry.path] is relative to the destination given to `withCopyToContainer`; an empty
 * path is the destination itself (a single file).
 */
internal class TarFiles(
    private val entries: List<Entry>,
) : Transferable {
    class Entry(
        val path: String,
        val bytes: ByteArray?,
        val mode: Int,
        val uid: Int = AGENT_UID,
    ) {
        val directory: Boolean get() = bytes == null
    }

    override fun getSize() = entries.sumOf { it.bytes?.size ?: 0 }.toLong()

    override fun getBytes() = ByteArray(0)

    override fun transferTo(
        tar: TarArchiveOutputStream,
        destination: String,
    ) {
        entries.forEach { e ->
            val name = if (e.path.isEmpty()) destination else "$destination/${e.path}"
            val entry = TarArchiveEntry(if (e.directory) "$name/" else name)
            // A name ending in "/" makes a directory entry; the mode holds the permission bits only.
            entry.mode = e.mode
            entry.setIds(e.uid, e.uid)
            e.bytes?.let { entry.size = it.size.toLong() }
            tar.putArchiveEntry(entry)
            e.bytes?.let(tar::write)
            tar.closeArchiveEntry()
        }
    }

    companion object {
        /** The image's non-root user (`USER 65532`, sard-agent: deploy/agent/Dockerfile). */
        const val AGENT_UID = 65532
        const val OWNER_ONLY = 0b110_000_000 // 0600
        const val READABLE = 0b110_100_100 // 0644
        const val OWNER_DIR = 0b111_000_000 // 0700

        /** One file owned by the agent, closed to everyone else, as the agent requires of secret files. */
        fun ownedByAgent(content: String) = TarFiles(listOf(Entry("", content.toByteArray(), OWNER_ONLY)))
    }
}
