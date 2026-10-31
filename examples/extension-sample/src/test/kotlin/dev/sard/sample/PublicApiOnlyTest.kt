// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.sample

import java.io.File
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

private val SERVER_IMPORT = Regex("""^import\s+dev\.sard\.server\.(\w+)""")

/** The sample stands for a third-party extension: it uses the public API, dev.sard.server.extension, and nothing else. */
class PublicApiOnlyTest {
    @Test
    fun `the sample imports only the public extension API of the server`() {
        val files = File("src/main/kotlin").walkTopDown().filter { it.isFile && it.extension == "kt" }.toList()
        assertTrue(files.size >= 5, "the scan found only ${files.size} files")
        val imports = files.flatMap { file -> file.readLines().filter { SERVER_IMPORT.containsMatchIn(it) }.map { file.name to it } }
        assertTrue(imports.isNotEmpty(), "the scan found no import of the server")
        val offenders = imports.filter { SERVER_IMPORT.find(it.second)?.groupValues?.get(1) != "extension" }
        assertEquals(emptyList(), offenders)
    }
}
