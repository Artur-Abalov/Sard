// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server

import java.io.File
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

private val ISOLATED_PACKAGES = listOf("enrollment", "persistence", "pki", "extension")
private val FORBIDDEN_FQN_REFERENCE =
    Regex("""\b(io\.grpc|dev\.sard\.proto|com\.google\.rpc|com\.google\.protobuf|dev\.sard\.server\.agents)\.""")
private val SESSIONS_SYSTEM_CALL = Regex("""\bsessions\.system\s*[({]""")
private val LINE_COMMENT = Regex("""//.*$""", RegexOption.MULTILINE)
private val BLOCK_COMMENT = Regex("""/\*.*?\*/""", RegexOption.DOT_MATCHES_ALL)

/** Source text with `//` and `/* */` comments stripped, so a comment mentioning a forbidden
 * package (e.g. in a KDoc example) never trips the scan, but any real reference — import or
 * fully-qualified use in a function body — does. */
private fun withoutComments(text: String): String = text.replace(BLOCK_COMMENT, "").replace(LINE_COMMENT, "")

/**
 * A dependency-free source scan, not a JVM classpath/reflection check: greps the .kt sources
 * under `src/main/kotlin`. Keeps two boundaries from ADR 0013 and the S2b review honest:
 *  a) `enrollment/`, `persistence/`, `pki/` and `extension/` never import the gRPC/protobuf
 *     boundary or the `agents/` package that adapts domain errors to it — those four packages
 *     stay usable without a gRPC server, wire format or the agents' translation layer.
 *  b) `TenantSessions.system` (the one call that bypasses the tenant filter) is used only where
 *     ADR 0013 lists it: `EnrollmentTokens.ownerOf` (a token before its tenant is known) and
 *     `AgentCertificateStandings.of` (a certificate by serial during each agent call, S3; the serials
 *     of all open streams on each stream check, S5a).
 */
class ArchitectureTest {
    private val mainRoot: File
        get() {
            val dir = File("src/main/kotlin/dev/sard/server")
            check(dir.isDirectory) { "expected to run with the server module as the working directory: $dir" }
            return dir
        }

    private fun ktFiles(root: File): List<File> =
        root
            .walkTopDown()
            .filter { it.isFile && it.extension == "kt" }
            .toList()

    @Test
    fun `enrollment, persistence, pki and extension reference nothing from the gRPC or agents boundary`() {
        val offenders = mutableListOf<String>()
        for (pkg in ISOLATED_PACKAGES) {
            for (file in ktFiles(File(mainRoot, pkg))) {
                val text = withoutComments(file.readText())
                val references = FORBIDDEN_FQN_REFERENCE.findAll(text).map { it.groupValues[1] }.toList()
                for (reference in references) {
                    offenders += "${file.path}: $reference."
                }
            }
        }
        assertTrue(offenders.isEmpty(), "forbidden references:\n${offenders.joinToString("\n")}")
    }

    @Test
    fun `sessions system is called only by the callers ADR 0013 lists`() {
        val allowed =
            listOf("agents/AgentCertificateStandings.kt", "enrollment/EnrollmentTokens.kt").map { File(mainRoot, it) }
        val callers = ktFiles(mainRoot).filter { SESSIONS_SYSTEM_CALL.containsMatchIn(it.readText()) }
        assertEquals(allowed.toSet(), callers.toSet(), "sessions.system callers must match ADR 0013's list exactly")
    }
}
