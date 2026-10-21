// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.fleet

import java.nio.file.Files
import java.nio.file.Path
import kotlin.streams.asSequence
import kotlin.test.Test
import kotlin.test.assertTrue

/** The package boundary of `fleet` (V3): its rules know nothing of the API, the agents' stream or the commands. */
class FleetImportsTest {
    private val forbidden =
        listOf(
            "dev.sard.server.selfbackup",
            "dev.sard.server.api",
            "dev.sard.server.agents",
            "dev.sard.proto",
            "org.springframework.web",
        )

    private fun sources(): List<Path> =
        Files.walk(Path.of("src/main/kotlin/dev/sard/server/fleet")).use { paths ->
            paths.asSequence().filter { it.toString().endsWith(".kt") }.toList()
        }

    @Test
    fun `fleet does not import the API, the self-backup, the agents' stream, the generated proto or Spring web`() {
        val files = sources()
        assertTrue(files.size > 1, "no source files of fleet found from ${Path.of("").toAbsolutePath()}")

        val offending =
            files.flatMap { file ->
                Files
                    .readAllLines(file)
                    .filter { it.trim().startsWith("import ") }
                    .filter { line -> forbidden.any { line.trim().removePrefix("import ").startsWith("$it.") } }
                    .map { "${file.fileName}: ${it.trim()}" }
            }

        assertTrue(offending.isEmpty(), "fleet imports what it must not: $offending")
    }
}
