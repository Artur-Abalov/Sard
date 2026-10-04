// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server

import java.io.File
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

private val ISOLATED_PACKAGES = listOf("enrollment", "persistence", "pki", "extension", "registration", "runs", "fleet")
private val FORBIDDEN_FQN_REFERENCE =
    Regex("""\b(io\.grpc|dev\.sard\.proto|com\.google\.rpc|com\.google\.protobuf|dev\.sard\.server\.agents)\.""")
private val SESSIONS_SYSTEM_CALL = Regex("""\bsessions\.system\s*[({]""")
private val LINE_COMMENT = Regex("""//.*$""", RegexOption.MULTILINE)
private val BLOCK_COMMENT = Regex("""/\*.*?\*/""", RegexOption.DOT_MATCHES_ALL)

// W1b (B1): the *Api ports (SessionApi and its siblings) stay implementable and
// testable without a servlet request or response, so an enterprise starter can
// replace one (e.g. SessionApi with SSO) without depending on this module's web layer.
private val API_PORT_FORBIDDEN_TYPE =
    Regex("""\b(HttpServletRequest|HttpServletResponse|ResponseEntity|ResponseCookie)\b""")
private val AUTH_PACKAGE_REFERENCE = Regex("""\bdev\.sard\.server\.auth\b""")
private val SESSION_DOMAIN_FORBIDDEN =
    Regex("""\b(jakarta\.servlet|org\.springframework\.http|org\.springframework\.web)\b""")
private val SESSION_DOMAIN_FILES =
    listOf("auth/SessionStore.kt", "auth/LoginAttemptTracker.kt", "auth/AdminPasswordAuthenticator.kt")

/** Source text with `//` and `/* */` comments stripped, so a comment mentioning a forbidden
 * package (e.g. in a KDoc example) never trips the scan, but any real reference — import or
 * fully-qualified use in a function body — does. */
private fun withoutComments(text: String): String = text.replace(BLOCK_COMMENT, "").replace(LINE_COMMENT, "")

/**
 * A dependency-free source scan, not a JVM classpath/reflection check: greps the .kt sources
 * under `src/main/kotlin`. Keeps two boundaries from ADR 0013 and the S2b review honest:
 *  a) `enrollment/`, `persistence/`, `pki/`, `extension/`, `registration/` and `runs/` never import the
 *     gRPC/protobuf boundary or the `agents/` package that adapts domain errors to it — those packages
 *     stay usable without a gRPC server, wire format or the agents' translation layer.
 *  b) `TenantSessions.system` (the one call that bypasses the tenant filter) is used only where
 *     ADR 0013 lists it: `EnrollmentTokens.ownerOf` (a token before its tenant is known) and
 *     `AgentCertificateStandings.of` (a certificate by serial during each agent call, S3; the serials
 *     of all open streams on each stream check, S5a) and `StepCounts.waiting` (the dispatch metric, S6a).
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
    fun `the domain packages reference nothing from the gRPC or agents boundary`() {
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
    fun `the api package references nothing from the gRPC or agents boundary`() {
        val offenders =
            ktFiles(File(mainRoot, "api")).flatMap { file ->
                FORBIDDEN_FQN_REFERENCE
                    .findAll(withoutComments(file.readText()))
                    .map { "${file.path}: ${it.groupValues[1]}." }
                    .toList()
            }
        assertTrue(offenders.isEmpty(), "api referencing the agent boundary:\n${offenders.joinToString("\n")}")
    }

    @Test
    fun `sessions system is called only by the callers ADR 0013 lists`() {
        val allowed =
            listOf(
                "agents/AgentCertificateStandings.kt",
                "enrollment/EnrollmentTokens.kt",
                "notify/Deliveries.kt",
                "runs/StepCounts.kt",
                "runs/StepDeadlines.kt",
            ).map { File(mainRoot, it) }
        val callers = ktFiles(mainRoot).filter { SESSIONS_SYSTEM_CALL.containsMatchIn(it.readText()) }
        assertEquals(allowed.toSet(), callers.toSet(), "sessions.system callers must match ADR 0013's list exactly")
    }

    @Test
    fun `StepResults takes partial from the verdict and does not judge the status itself`() {
        val text = withoutComments(File(mainRoot, "runs/StepResults.kt").readText())

        assertTrue("StepState.SUCCEEDED" !in text, "partial is decided by ResultCheck only (ADR 0034)")
    }

    /** The text of every `interface *Api { ... }` block, braces balanced, in [file]. */
    private fun apiPortBlocks(file: File): List<String> {
        val text = withoutComments(file.readText())
        val results = mutableListOf<String>()
        for (match in Regex("""interface \w*Api\b[^{]*\{""").findAll(text)) {
            var depth = 1
            var i = match.range.last + 1
            while (i < text.length && depth > 0) {
                when (text[i]) {
                    '{' -> depth++
                    '}' -> depth--
                }
                i++
            }
            results += text.substring(match.range.first, i)
        }
        return results
    }

    @Test
    fun `the Api ports declare no HTTP request, response or Spring web types`() {
        val offenders = mutableListOf<String>()
        for (file in ktFiles(File(mainRoot, "api"))) {
            for (block in apiPortBlocks(file)) {
                val hits = API_PORT_FORBIDDEN_TYPE.findAll(block).map { it.value }.toSet()
                if (hits.isNotEmpty()) offenders += "${file.path}: $hits"
            }
        }
        assertTrue(offenders.isEmpty(), "api ports depend on HTTP types:\n${offenders.joinToString("\n")}")
    }

    @Test
    fun `the api package never references the auth package`() {
        val offenders =
            ktFiles(File(mainRoot, "api")).filter {
                AUTH_PACKAGE_REFERENCE.containsMatchIn(withoutComments(it.readText()))
            }
        assertTrue(offenders.isEmpty(), "api referencing auth:\n${offenders.joinToString("\n") { it.path }}")
    }

    @Test
    fun `the session domain classes have no HTTP or Spring web dependency`() {
        val offenders =
            SESSION_DOMAIN_FILES
                .map { File(mainRoot, it) }
                .filter { SESSION_DOMAIN_FORBIDDEN.containsMatchIn(withoutComments(it.readText())) }
        val message = "session domain classes depend on HTTP:\n${offenders.joinToString("\n") { it.path }}"
        assertTrue(offenders.isEmpty(), message)
    }
}
