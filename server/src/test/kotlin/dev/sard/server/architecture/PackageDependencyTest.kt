// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.architecture

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

/** The slices (first package segment below dev.sard.server) a slice imports from, with the file doing it. */
private fun importsOf(slice: String): List<Pair<String, String>> =
    kotlinFiles()
        .flatMap { file ->
            val lines = file.readLines()
            val own = lines.firstNotNullOfOrNull { PACKAGE.find(it)?.groupValues?.get(1) }
            val imported = lines.mapNotNull { IMPORT.find(it)?.groupValues?.get(1) }
            if (own == slice) imported.map { file.name to it } else emptyList()
        }.toList()

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
    fun `the loop package is a leaf of the server`() {
        assertTrue(MAIN.resolve("loop/PeriodicTask.kt").isFile)
        assertEquals(emptySet(), importedBy("loop"))
    }

    @Test
    fun `the pki package depends on no other package of the server except the role and the loop`() {
        // The scan finds pki's imports, so an empty result below is not a scan that saw nothing.
        assertEquals(setOf("role", "loop"), importedBy("pki"))
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
    fun `the metrics listener is outside Spring Security and the servlet stack`() {
        val outside = Regex("""^import\s+(org\.springframework\.security|jakarta\.servlet|org\.springframework\.web)""")
        for (slice in listOf("metrics", "prometheus")) {
            val files = MAIN.resolve(slice).listFiles { f -> f.extension == "kt" }.orEmpty()
            assertTrue(files.size >= 5, "the scan found only ${files.size} files of $slice")
            val offenders =
                files.flatMap { f -> f.readLines().filter { outside.containsMatchIn(it) }.map { "${f.name}: $it" } }
            assertEquals(emptyList(), offenders)
        }
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

private val LICENCE_SEAM =
    Regex("""^import\s+dev\.sard\.server\.extension\.(\*|Edition\w*|ReportedEdition|Licence\w*)""")

private val LICENCE_ROOTS =
    listOf("runs", "agents", "verify", "scheduler", "selfbackup", "freshness", "retention", "fleet")

/** The slices [roots] import from, directly or through others, and the roots themselves. */
private fun closureOf(roots: List<String>): Set<String> {
    val seen = roots.toMutableSet()
    val queue = ArrayDeque(roots)
    while (queue.isNotEmpty()) {
        importedBy(queue.removeFirst()).filter { seen.add(it) }.forEach { queue.add(it) }
    }
    return seen
}

private fun filesOfSlice(slice: String) = kotlinFiles(MAIN.resolve(slice))

private fun filesImportingLicenceSeam(slices: Collection<String>) =
    slices.flatMap { slice ->
        val files = filesOfSlice(slice)
        assertTrue(files.isNotEmpty(), "the scan found no files of $slice")
        files.filter { file -> file.readLines().any { LICENCE_SEAM.containsMatchIn(it) } }.map { it.path }
    }

/** Rule "Лицензия никогда не блокирует бэкап, восстановление и проверку" (server-core-extensions.feature). */
class LicenceNeverBlocksTest {
    @Test
    fun `Запуски, отправка, проверка, планировщик и восстановление не зависят от шва лицензии`() {
        assertEquals(emptyList(), filesImportingLicenceSeam(LICENCE_ROOTS))
    }

    /**
     * The slices those depend on are checked too. The extension slice itself is the carrier of the seam and of the
     * run events: it is a leaf (ExtensionApiLeafTest), so it adds no edge, and its files need no import of the seam.
     */
    @Test
    fun `Срезы, от которых зависят эти срезы, тоже не зависят от шва лицензии`() {
        val closure = closureOf(LICENCE_ROOTS) - "extension"
        assertTrue(closure.containsAll(LICENCE_ROOTS), "the closure lost a root: $closure")
        assertTrue(closure.size > LICENCE_ROOTS.size, "the scan followed no import: $closure")
        assertEquals(emptyList(), filesImportingLicenceSeam(closure))
    }
}

/** The public extension API is a leaf: it imports no other slice of the server. */
class ExtensionApiLeafTest {
    @Test
    fun `Публичный API расширений не импортирует другие срезы сервера`() {
        assertTrue(filesOfSlice("extension").size > 5, "the scan found too few files of extension")
        assertEquals(emptySet(), importedBy("extension"))
    }
}

private val SCHEDULER_FACTORY = Regex("""\bnewSingleThreadScheduledExecutor\b""")

/** The periodic loops share one implementation, so a thread of their own is made in one place. */
class SingleSchedulerFactoryTest {
    @Test
    fun `only the loop package makes a single thread scheduler`() {
        val files = kotlinFiles()
        assertTrue(files.size > 50, "the scan found only ${files.size} files")
        val makers =
            files
                .filter { file -> file.readLines().any { SCHEDULER_FACTORY.containsMatchIn(it) } }
                .map { it.name }
        assertEquals(listOf("PeriodicTask.kt"), makers)
    }

    @Test
    fun `The scan recognises a call and a reference, and nothing else`() {
        for (made in listOf("Executors.newSingleThreadScheduledExecutor(f)", "::newSingleThreadScheduledExecutor")) {
            assertTrue(SCHEDULER_FACTORY.containsMatchIn(made), made)
        }
        for (other in listOf("Executors.newSingleThreadExecutor()", "newScheduledThreadPool(2)")) {
            assertTrue(!SCHEDULER_FACTORY.containsMatchIn(other), other)
        }
    }
}

private val EDITION_CALL = Regex("""\.edition\(\)|::edition\b""")

/** Rule "Редакция читается один раз при старте" (server-core-extensions.feature). */
class EditionReadOnceTest {
    @Test
    fun `Only the edition auto-configuration reads the edition of the seam`() {
        val files = kotlinFiles()
        assertTrue(files.size > 50, "the scan found only ${files.size} files")
        val callers =
            files
                .filter { file -> file.readLines().any { EDITION_CALL.containsMatchIn(it) } }
                .map { it.name }
        assertEquals(listOf("EditionAutoConfiguration.kt"), callers)
    }

    @Test
    fun `The scan recognises a call and a method reference to edition, and nothing else`() {
        for (read in listOf("provider.edition()", "provider?.edition()", "val f = provider::edition", "::edition")) {
            assertTrue(EDITION_CALL.containsMatchIn(read), read)
        }
        for (other in listOf("provider.licence()", "val editionId = 1", "provider::editionName", "Edition.FREE")) {
            assertTrue(!EDITION_CALL.containsMatchIn(other), other)
        }
    }
}
