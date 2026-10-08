// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.architecture

import java.io.File
import kotlin.test.Test
import kotlin.test.assertEquals

private const val ROOT = "dev.sard.server"
private val SOURCE_ROOT = File("src/main/kotlin/dev/sard/server")
private val PACKAGE = Regex("""^package\s+${Regex.escape(ROOT)}\.(\w+)""")
private val IMPORT = Regex("""^import\s+${Regex.escape(ROOT)}\.(\w+)\.""")

/** Edges `a -> b` between top-level slices: a file of `a` imports from `b`. */
private fun sliceEdges(): Set<Pair<String, String>> =
    SOURCE_ROOT
        .walkTopDown()
        .filter { it.isFile && it.extension == "kt" }
        .flatMap { file ->
            val lines = file.readLines()
            val slice = lines.firstNotNullOfOrNull { PACKAGE.find(it)?.groupValues?.get(1) }
            lines.mapNotNull { IMPORT.find(it)?.groupValues?.get(1) }.mapNotNull { slice?.to(it) }
        }.filter { (from, to) -> from != to }
        .toSet()

/** Edges that lie on a cycle: `a -> b` where `a` is reachable from `b`. */
private fun cyclicEdges(edges: Set<Pair<String, String>>): List<String> {
    fun reachable(start: String): Set<String> {
        val seen = mutableSetOf<String>()
        val queue = ArrayDeque(listOf(start))
        while (queue.isNotEmpty()) {
            val next = queue.removeFirst()
            edges.filter { it.first == next && seen.add(it.second) }.forEach { queue.add(it.second) }
        }
        return seen
    }
    return edges.filter { (from, to) -> from in reachable(to) }.map { (from, to) -> "$from -> $to" }.sorted()
}

class PackageCycleTest {
    @Test
    fun `top-level slices of the server do not depend on each other in a cycle`() {
        assertEquals(emptyList(), cyclicEdges(sliceEdges()))
    }

    @Test
    fun `a cycle is reported by its edges`() {
        val edges = setOf("a" to "b", "b" to "a", "b" to "c")
        assertEquals(listOf("a -> b", "b -> a"), cyclicEdges(edges))
    }
}
