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

private val FRAMEWORK_IMPORT =
    Regex(
        """^import\s+(org\.springframework|jakarta\.|javax\.|org\.hibernate|io\.grpc|dev\.sard\.proto""" +
            """|com\.sun\.net\.httpserver)""",
    )

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

    @Test
    fun `the metrics package is domain code without framework, HTTP server, gRPC or persistence imports`() {
        val files = MAIN.resolve("metrics").listFiles { f -> f.extension == "kt" }.orEmpty()
        assertTrue(files.size >= 5, "the scan found only ${files.size} files of metrics")
        val offenders =
            files.flatMap { file ->
                file.readLines().filter { FRAMEWORK_IMPORT.containsMatchIn(it) }.map { "${file.name}: $it" }
            }
        assertEquals(emptyList(), offenders)
    }

    @Test
    fun `selfbackup knows metrics and metrics does not know selfbackup`() {
        assertTrue("metrics" in importedBy("selfbackup"))
        assertTrue("selfbackup" !in importedBy("metrics"))
    }

    @Test
    fun `the prometheus adapter depends on metrics and metrics does not depend on it`() {
        assertTrue("metrics" in importedBy("prometheus"))
        assertTrue("prometheus" !in importedBy("metrics"))
    }
}

private val LICENCE_SEAM = Regex("""^import\s+dev\.sard\.server\.extension\.(Edition|EditionProvider|Licence\w*)\b""")

/** Rule "Лицензия никогда не блокирует бэкап, восстановление и проверку" (server-core-extensions.feature). */
class LicenceNeverBlocksTest {
    @Test
    fun `Запуски, отправка, проверка, планировщик и восстановление не зависят от шва лицензии`() {
        val slices = listOf("runs", "agents", "verify", "scheduler", "selfbackup", "freshness", "retention", "fleet")
        val offenders =
            slices.flatMap { slice ->
                val files =
                    MAIN
                        .resolve(slice)
                        .walkTopDown()
                        .filter { it.isFile && it.extension == "kt" }
                        .toList()
                assertTrue(files.isNotEmpty(), "the scan found no files of $slice")
                files.filter { file -> file.readLines().any { LICENCE_SEAM.containsMatchIn(it) } }.map { it.path }
            }
        assertEquals(emptyList(), offenders)
    }
}

private val EDITION_CALL = Regex("""\.edition\(\)""")

/** Rule "Редакция читается один раз при старте" (server-core-extensions.feature). */
class EditionReadOnceTest {
    @Test
    fun `Only the edition auto-configuration reads the edition of the seam`() {
        val files = MAIN.walkTopDown().filter { it.isFile && it.extension == "kt" }.toList()
        assertTrue(files.size > 50, "the scan found only ${files.size} files")
        val callers =
            files
                .filter { file -> file.readLines().any { EDITION_CALL.containsMatchIn(it) } }
                .map { it.name }
        assertEquals(listOf("EditionAutoConfiguration.kt"), callers)
    }
}
