// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.runs

/** What restic counted in a snapshot: its files and their bytes. */
data class SnapshotTotals(
    val files: Long,
    val bytes: Long,
)

/**
 * The warning of a backup that is much smaller than the last whole snapshot of its source (V2, design: «пройдено с
 * предупреждением»): a disk that went missing, a directory emptied by mistake. The backup succeeds; the warning
 * is shown at its run.
 */
object VolumeDrop {
    /** A fall by more than this share of the last snapshot is a warning. */
    private const val PERCENT = 30
    private const val HUNDRED = 100

    fun warnings(
        previous: SnapshotTotals?,
        current: SnapshotTotals,
    ): List<String> {
        previous ?: return emptyList()
        return listOfNotNull(
            fall(previous.files, current.files)?.let {
                "the number of files fell by $it% since the last snapshot (${previous.files} → ${current.files})"
            },
            fall(previous.bytes, current.bytes)?.let {
                "the size fell by $it% since the last snapshot (${previous.bytes} → ${current.bytes} bytes)"
            },
        )
    }

    /** The percent [current] fell by from [previous] when it is more than [PERCENT], else null. */
    private fun fall(
        previous: Long,
        current: Long,
    ): Long? {
        val percent = if (previous > 0) (previous - current) * HUNDRED / previous else 0
        return percent.takeIf { (previous - current) * HUNDRED > previous * PERCENT }
    }
}
