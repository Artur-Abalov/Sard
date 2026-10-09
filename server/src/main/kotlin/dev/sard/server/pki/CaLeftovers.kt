// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.pki

import java.nio.file.Files
import java.nio.file.NoSuchFileException
import java.nio.file.Path
import java.nio.file.StandardCopyOption.ATOMIC_MOVE
import java.time.Clock
import java.time.Duration
import java.time.Instant

/** A staging directory this old was left by a crashed first start, not by one still running. */
private val STALE_STAGING = Duration.ofHours(1)

/** What a start that died leaves in the CA directory, and what the next start does about it. */
internal class CaLeftovers(
    private val dir: Path,
    private val clock: Clock,
) {
    private val ca = dir.resolve(CA)

    fun tidy() {
        restoreInterruptedReplacement()
        removeStaleStaging()
    }

    /** No `ca` but the CA that was swapped away: the replacement died half way, the old CA is the CA. */
    private fun restoreInterruptedReplacement() {
        val swapped = entries { it.fileName.toString().startsWith(REPLACED) }
        if (Files.notExists(ca)) swapped.firstOrNull()?.let { Files.move(it, ca, ATOMIC_MOVE) }
        entries { it.fileName.toString().startsWith(REPLACED) }.forEach { it.toFile().deleteRecursively() }
    }

    private fun entries(matching: (Path) -> Boolean): List<Path> = Files.list(dir).use { it.filter(matching).toList() }

    private fun removeStaleStaging() {
        val cutoff = clock.instant() - STALE_STAGING
        entries { it.fileName.toString().startsWith(STAGING) && isStale(it, cutoff) }
            .forEach { it.toFile().deleteRecursively() }
    }

    /** Gone already (a concurrent start published or removed it): not ours to clean. Other failures stop the start. */
    private fun isStale(
        path: Path,
        cutoff: Instant,
    ): Boolean =
        try {
            Files.getLastModifiedTime(path).toInstant() < cutoff
        } catch (_: NoSuchFileException) {
            false
        }
}
