// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.architecture

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

/** Edges `a -> b` between top-level slices: a file of `a` imports from `b`. */
private fun sliceEdges(): Set<Pair<String, String>> =
    kotlinFiles()
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
        val edges = sliceEdges()
        // A scan that found nothing would pass vacuously; this edge is known to exist.
        assertTrue("enrollment" to "pki" in edges, "the scan found no enrollment -> pki edge: $edges")
        assertEquals(emptyList(), cyclicEdges(edges))
    }

    @Test
    fun `a cycle is reported by its edges`() {
        val edges = setOf("a" to "b", "b" to "a", "b" to "c")
        assertEquals(listOf("a -> b", "b -> a"), cyclicEdges(edges))
    }
}
