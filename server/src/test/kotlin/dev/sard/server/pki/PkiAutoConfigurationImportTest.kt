// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.pki

import dev.sard.server.enrollment.AgentEndpointConfiguration
import dev.sard.server.enrollment.InvalidAgentEndpointException
import org.junit.jupiter.api.io.TempDir
import org.springframework.boot.autoconfigure.AutoConfigurations
import org.springframework.boot.test.context.runner.ApplicationContextRunner
import org.springframework.context.annotation.Bean
import org.springframework.context.annotation.Configuration
import java.nio.file.Files
import java.nio.file.Path
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNotEquals
import kotlin.test.assertNotNull
import kotlin.test.assertTrue

/** Needs the CA before anything else does, like the beans that sign or verify certificates. */
@Configuration(proxyBeanMethods = false)
private class EagerCaConfiguration {
    @Bean
    fun early(ca: CertificateAuthority) = ca.fingerprint().hex
}

/** The startup of the server with `sard.pki.import-dir`: the setting reaches the CA, an empty one does not. */
class PkiAutoConfigurationImportTest {
    @TempDir
    lateinit var tmp: Path

    private val dir get() = tmp.resolve("pki")
    private val importDir get() = tmp.resolve("import")
    private val original = CaImportFixtures.original()

    private fun runner() =
        ApplicationContextRunner()
            .withConfiguration(AutoConfigurations.of(PkiAutoConfiguration::class.java))
            .withPropertyValues(
                "sard.pki.dir=$dir",
                "sard.pki.server-names=sard.example.com,localhost",
                "spring.grpc.server.port=9090",
            )

    private fun fingerprintOf(vararg properties: String): String? {
        var fingerprint: String? = null
        runner().withPropertyValues(*properties).run { context ->
            assertEquals(null, context.startupFailure)
            fingerprint = context.getBean(CertificateAuthority::class.java).fingerprint().hex
        }
        return fingerprint
    }

    @Test
    fun `sard pki import-dir makes the first start take that CA`() {
        CaImportFixtures.source(importDir, original)
        assertEquals(CaFingerprint.of(original.certificate).hex, fingerprintOf("sard.pki.import-dir=$importDir"))
    }

    @Test
    fun `an empty or missing import-dir leaves the CA to be generated as before`() {
        val fingerprint = CaFingerprint.of(original.certificate).hex
        assertNotEquals(fingerprint, fingerprintOf("sard.pki.import-dir="))
        Files.walk(dir).use { it.sorted(Comparator.reverseOrder()).forEach(Files::delete) }
        assertNotEquals(fingerprint, fingerprintOf())
    }

    @Test
    fun `a refused import is a startup failure that carries the refusal`() {
        CaImportFixtures.source(importDir, original)
        CaImportFixtures.chmod(importDir.resolve("ca/ca.key"), "rw-r-----")
        var failure: Throwable? = null
        runner().withPropertyValues("sard.pki.import-dir=$importDir").run { failure = it.startupFailure }
        val refused = generateSequence(failure) { it.cause }.filterIsInstance<CaImportRefused>().firstOrNull()
        assertNotNull(refused, "no CaImportRefused in $failure")
        assertEquals(CaImportRefusal.IMPORT_PERMISSIONS_TOO_OPEN, refused.reason)
    }

    @Test
    fun `an address for agents outside the server names stops the start before any CA is imported`() {
        CaImportFixtures.source(importDir, original)
        var failure: Throwable? = null
        runner()
            .withUserConfiguration(EagerCaConfiguration::class.java, AgentEndpointConfiguration::class.java)
            .withPropertyValues("sard.pki.import-dir=$importDir", "sard.agent.endpoint=old.example.com:9090")
            .run { failure = it.startupFailure }
        assertTrue(generateSequence(failure) { it.cause }.any { it is InvalidAgentEndpointException }, "$failure")
        assertTrue(Files.notExists(dir.resolve("ca")), "the CA was imported before the address was checked")
    }
}
