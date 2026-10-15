// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.architecture

import java.io.File
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

private const val ROOT = "dev.sard.server"
private val MAIN = File("src/main/kotlin/dev/sard/server")
private val PACKAGE = Regex("""^package\s+${Regex.escape(ROOT)}\.(\w+)""")
private val IMPORT = Regex("""^import\s+${Regex.escape(ROOT)}\.(\w+)\.""")

/** The slices (first package segment below dev.sard.server) a slice imports from, with the file doing it. */
private fun importsOf(slice: String): List<Pair<String, String>> {
    check(MAIN.isDirectory) { "expected to run with the server module as the working directory: $MAIN" }
    return MAIN
        .walkTopDown()
        .filter { it.isFile && it.extension == "kt" }
        .flatMap { file ->
            val lines = file.readLines()
            val own = lines.firstNotNullOfOrNull { PACKAGE.find(it)?.groupValues?.get(1) }
            val imported = lines.mapNotNull { IMPORT.find(it)?.groupValues?.get(1) }
            if (own == slice) imported.map { file.name to it } else emptyList()
        }.toList()
}

private fun importedBy(slice: String): Set<String> = importsOf(slice).map { it.second }.filter { it != slice }.toSet()

class PackageDependencyTest {
    @Test
    fun `the role package is a leaf of the server`() {
        assertTrue(MAIN.resolve("role/AgentRole.kt").isFile)
        assertEquals(emptySet(), importedBy("role"))
    }

    @Test
    fun `the pki package depends on no other package of the server except the role`() {
        // The scan finds pki's one import, so an empty result below is not a scan that saw nothing.
        assertEquals(setOf("role"), importedBy("pki"))
    }

    @Test
    fun `the domain slices do not know about certificates`() {
        for (slice in listOf("runs", "fleet", "scheduler", "registration")) {
            assertTrue(importsOf(slice).isNotEmpty(), "the scan found no imports of $slice")
            val viaPki = importsOf(slice).filter { it.second == "pki" }
            assertTrue(viaPki.isEmpty(), "$slice imports pki: $viaPki")
        }
    }
}
