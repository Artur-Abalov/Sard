// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.e2e

import kotlin.random.Random

/**
 * A directory tree of known content to back up, generated from a fixed seed. The reference the
 * restored tree is compared with ([expected]) is taken here, when the bytes are made, before
 * anything is copied to the agent host or backed up.
 *
 * A path is relative to the tree's root; a directory's content is null.
 */
internal class SourceTree private constructor(
    private val entries: List<Item>,
) {
    private class Item(
        val path: String,
        val bytes: ByteArray?,
        val excluded: Boolean = false,
        val unreadable: Boolean = false,
    )

    /** What a restore must give back: every entry that is neither excluded nor unreadable. */
    val expected: Map<String, ByteArray?> =
        entries.filterNot { it.excluded || it.unreadable }.associate { it.path to it.bytes?.copyOf() }

    /** The tree as files owned by the agent, except an unreadable file: root's, mode 0000. */
    fun files(): TarFiles =
        TarFiles(
            listOf(TarFiles.Entry("", null, TarFiles.OWNER_DIR)) +
                entries.map { item ->
                    when {
                        item.unreadable -> TarFiles.Entry(item.path, item.bytes, mode = 0, uid = 0)
                        item.bytes == null -> TarFiles.Entry(item.path, null, TarFiles.OWNER_DIR)
                        else -> TarFiles.Entry(item.path, item.bytes, TarFiles.OWNER_ONLY)
                    }
                },
        )

    /** The paths restic must leave out, as files plugin `exclude` patterns for a tree at [root]. */
    fun excludePatterns(root: String): List<String> = listOf("$root/$EXCLUDED_DIR", "*$EXCLUDED_SUFFIX")

    /** The unreadable file's path under [root], or null. */
    fun unreadablePath(root: String): String? = entries.firstOrNull { it.unreadable }?.let { "$root/${it.path}" }

    companion object {
        private const val EXCLUDED_DIR = "cache"
        private const val EXCLUDED_SUFFIX = ".tmp"
        private const val KIB = 1024
        private const val MIB = 1024 * KIB

        /**
         * Sizes from empty to larger than a restic chunk (512 KiB minimum), nesting three levels
         * deep, a name with a space and non-ASCII letters, one excluded directory and one file
         * excluded by suffix. With [unreadable], one more file the agent cannot read.
         */
        fun generate(
            seed: Int,
            unreadable: Boolean = false,
        ): SourceTree {
            val random = Random(seed)
            fun bytes(size: Int) = random.nextBytes(size)
            val items =
                mutableListOf(
                    Item("empty.txt", ByteArray(0)),
                    Item("one.bin", bytes(1)),
                    Item("small.txt", "line of text\n".repeat(KIB / 4).toByteArray().copyOf(4 * KIB)),
                    Item("big.bin", bytes(3 * MIB + 17)),
                    Item("nested", null),
                    Item("nested/level1", null),
                    Item("nested/level1/level2", null),
                    Item("nested/level1/level2/deep.txt", bytes(777)),
                    Item("nested/имя с пробелом.txt", "не ASCII\n".toByteArray()),
                    Item(EXCLUDED_DIR, null, excluded = true),
                    Item("$EXCLUDED_DIR/skip.bin", bytes(2 * KIB), excluded = true),
                    Item("notes$EXCLUDED_SUFFIX", bytes(100), excluded = true),
                )
            if (unreadable) items += Item("locked.bin", bytes(KIB), unreadable = true)
            return SourceTree(items)
        }
    }
}

/** The differences between a reference tree and a restored one; empty when they are byte for byte equal. */
internal object TreeDiff {
    fun of(
        expected: Map<String, ByteArray?>,
        actual: Map<String, ByteArray?>,
    ): List<String> =
        (expected.keys + actual.keys).sorted().mapNotNull { path ->
            val want = expected[path]
            val got = actual[path]
            when {
                path !in actual -> "missing: $path"
                path !in expected -> "unexpected: $path"
                (want == null) != (got == null) -> "type differs: $path"
                want != null && !want.contentEquals(got) -> "content differs: $path"
                else -> null
            }
        }
}
