// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server

import java.io.File
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

private val ISOLATED_PACKAGES = listOf("enrollment", "persistence", "pki", "extension")
private val FORBIDDEN_IMPORT_PREFIXES =
    listOf(
        "io.grpc.",
        "dev.sard.proto.",
        "com.google.rpc.",
        "com.google.protobuf.",
        "dev.sard.server.agents.",
    )
private val IMPORT_LINE = Regex("""^import\s+([\w.]+)""", RegexOption.MULTILINE)
private val SESSIONS_SYSTEM_CALL = Regex("""\bsessions\.system\s*[({]""")

/**
 * A dependency-free source scan, not a JVM classpath/reflection check: greps the .kt sources
 * under `src/main/kotlin`. Keeps two boundaries from ADR 0013 and the S2b review honest:
 *  a) `enrollment/`, `persistence/`, `pki/` and `extension/` never import the gRPC/protobuf
 *     boundary or the `agents/` package that adapts domain errors to it — those four packages
 *     stay usable without a gRPC server, wire format or the agents' translation layer.
 *  b) `TenantSessions.system` (the one call that bypasses the tenant filter) is used only where
 *     ADR 0013 lists it: `EnrollmentTokens.ownerOf`, looking a token up before its tenant is known.
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
    fun `enrollment, persistence, pki and extension import nothing from the gRPC or agents boundary`() {
        val offenders = mutableListOf<String>()
        for (pkg in ISOLATED_PACKAGES) {
            for (file in ktFiles(File(mainRoot, pkg))) {
                val imports = IMPORT_LINE.findAll(file.readText()).map { it.groupValues[1] }
                for (import in imports) {
                    if (FORBIDDEN_IMPORT_PREFIXES.any { import.startsWith(it) }) {
                        offenders += "${file.path}: import $import"
                    }
                }
            }
        }
        assertTrue(offenders.isEmpty(), "forbidden imports:\n${offenders.joinToString("\n")}")
    }

    @Test
    fun `sessions system is called only in enrollment EnrollmentTokens (ADR 0013)`() {
        val allowed = File(mainRoot, "enrollment/EnrollmentTokens.kt")
        val callers = ktFiles(mainRoot).filter { SESSIONS_SYSTEM_CALL.containsMatchIn(it.readText()) }
        assertEquals(listOf(allowed), callers, "sessions.system callers must match ADR 0013's list exactly")
    }
}
