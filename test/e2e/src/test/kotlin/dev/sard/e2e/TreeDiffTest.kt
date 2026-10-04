// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.e2e

import kotlin.test.Test
import kotlin.test.assertEquals

/**
 * The comparison [FullChainTest] relies on, without containers: if it let a damaged restore
 * through, the chain test would prove nothing.
 */
class TreeDiffTest {
    private val reference = SourceTree.generate(SEED).expected

    @Test
    fun `an identical tree has no differences`() {
        assertEquals(emptyList(), TreeDiff.of(reference, copy()))
    }

    @Test
    fun `one changed byte in the largest file is a difference`() {
        val restored = copy().apply { getValue("big.bin")!![BYTE] = (getValue("big.bin")!![BYTE] + 1).toByte() }
        assertEquals(listOf("content differs: big.bin"), TreeDiff.of(reference, restored))
    }

    @Test
    fun `a missing, an extra and a truncated file are differences`() {
        val restored =
            copy().apply {
                remove("nested/level1/level2/deep.txt")
                put("cache/skip.bin", ByteArray(1))
                put("small.txt", getValue("small.txt")!!.copyOf(10))
            }
        assertEquals(
            listOf("content differs: small.txt", "missing: nested/level1/level2/deep.txt", "unexpected: cache/skip.bin").sorted(),
            TreeDiff.of(reference, restored).sorted(),
        )
    }

    @Test
    fun `a file restored where a directory was is a difference`() {
        val restored = copy().apply { put("nested/level1", ByteArray(0)) }
        assertEquals(listOf("type differs: nested/level1"), TreeDiff.of(reference, restored))
    }

    @Test
    fun `the reference leaves out excluded and unreadable entries and is the same for the same seed`() {
        val tree = SourceTree.generate(SEED, unreadable = true)
        assertEquals(emptyList(), TreeDiff.of(reference, tree.expected))
        assertEquals(setOf("big.bin", "empty.txt", "one.bin", "small.txt"), reference.keys.filter { '/' !in it && reference[it] != null }.toSet())
    }

    private fun copy(): MutableMap<String, ByteArray?> = reference.mapValuesTo(linkedMapOf()) { it.value?.copyOf() }

    private companion object {
        const val SEED = 20261004
        const val BYTE = 1_000_000
    }
}
