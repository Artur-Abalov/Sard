// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.runs

import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import kotlin.test.Test
import kotlin.test.assertEquals

/** A backup much smaller than the last snapshot of its source passes with a warning (V2, the 30 % rule). */
@MutFlowTest
class VolumeDropTest {
    private fun warnings(
        previous: SnapshotTotals?,
        current: SnapshotTotals,
    ) = MutFlow.underTest { VolumeDrop.warnings(previous, current) }

    @Test
    fun `a fall of the files or the bytes by more than 30 percent is a warning each`() {
        assertEquals(
            listOf(
                "the number of files fell by 31% since the last snapshot (1000 → 690)",
                "the size fell by 50% since the last snapshot (4000 → 2000 bytes)",
            ),
            warnings(SnapshotTotals(1000, 4000), SnapshotTotals(690, 2000)),
        )
    }

    @Test
    fun `a fall of exactly 30 percent, a small one or a growth is no warning`() {
        for (current in listOf(SnapshotTotals(700, 2800), SnapshotTotals(999, 3999), SnapshotTotals(5000, 9000))) {
            assertEquals(emptyList(), warnings(SnapshotTotals(1000, 4000), current), current.toString())
        }
    }

    @Test
    fun `a fall to nothing is a fall of 100 percent`() {
        assertEquals(
            listOf("the number of files fell by 100% since the last snapshot (3 → 0)"),
            warnings(SnapshotTotals(3, 0), SnapshotTotals(0, 0)),
        )
    }

    // The last whole snapshot may be an older agent's: its size is known, its number of files is not.
    @Test
    fun `a previous snapshot without a number of files is compared by its size alone`() {
        assertEquals(
            listOf("the size fell by 50% since the last snapshot (4000 → 2000 bytes)"),
            warnings(SnapshotTotals(null, 4000), SnapshotTotals(1, 2000)),
        )
        assertEquals(emptyList(), warnings(SnapshotTotals(null, 4000), SnapshotTotals(1, 4000)))
    }

    // An older agent's backup brings its size, not its number of files: the size is compared all the same.
    @Test
    fun `a backup without a number of files is compared by its size alone`() {
        assertEquals(
            listOf("the size fell by 50% since the last snapshot (4000 → 2000 bytes)"),
            warnings(SnapshotTotals(10, 4000), SnapshotTotals(null, 2000)),
        )
    }

    @Test
    fun `without a previous snapshot, or one of nothing, there is nothing to fall from`() {
        assertEquals(emptyList(), warnings(null, SnapshotTotals(0, 0)))
        assertEquals(emptyList(), warnings(SnapshotTotals(0, 0), SnapshotTotals(0, 0)))
    }
}
