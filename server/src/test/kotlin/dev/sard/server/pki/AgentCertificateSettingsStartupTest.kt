// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.pki

import dev.sard.server.pki.PkiFixtures.AGENT
import dev.sard.server.pki.PkiFixtures.certificate
import dev.sard.server.pki.PkiFixtures.resource
import dev.sard.server.role.AgentRole
import org.junit.jupiter.api.io.TempDir
import org.springframework.boot.autoconfigure.AutoConfigurations
import org.springframework.boot.test.context.runner.ApplicationContextRunner
import java.nio.file.Path
import java.time.Clock
import java.time.Duration
import java.time.Instant
import java.time.ZoneOffset
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNotNull
import kotlin.test.assertTrue

private val T0 = Instant.parse("2026-10-15T12:00:00Z")

/** C1, rule "Срок сертификата агента настраивается и не выходит за срок CA": the server's start. */
class AgentCertificateSettingsStartupTest {
    @TempDir
    lateinit var tmp: Path

    private fun runner() =
        ApplicationContextRunner()
            .withConfiguration(AutoConfigurations.of(PkiAutoConfiguration::class.java))
            .withBean(CaLedger::class.java, { FakeCaLedger() })
            .withBean(Clock::class.java, { Clock.fixed(T0, ZoneOffset.UTC) })
            .withPropertyValues(
                "sard.pki.dir=${tmp.resolve("pki")}",
                "sard.pki.server-names=sard.example.com,localhost",
                "spring.grpc.server.port=9090",
            )

    private fun startupFailure(vararg properties: String): String {
        var message: String? = null
        runner().withPropertyValues(*properties).run { context ->
            val failure = assertNotNull(context.startupFailure, "the context started")
            message =
                generateSequence<Throwable>(failure) { it.cause }.filterIsInstance<InvalidPkiSettingException>().firstOrNull()?.message
        }
        return assertNotNull(message, "no InvalidPkiSettingException in the startup failure")
    }

    @Test
    fun `Без настроек действуют 365 суток и интервал один час`() {
        runner().run { context ->
            assertEquals(null, context.startupFailure)
            val policy = context.getBean(AgentCertificatePolicy::class.java)
            assertEquals(Duration.ofDays(365), policy.validity)
            assertEquals(Duration.ofHours(1), policy.effectiveInterval)
        }
    }

    @Test
    fun `Срок ровно 10 минут допустим и доходит до выпуска сертификата по времени сервера`() {
        runner().withPropertyValues("sard.pki.agent-certificate-validity=PT10M").run { context ->
            assertEquals(null, context.startupFailure)
            val ca = context.getBean(CertificateAuthority::class.java)
            val issued = ca.issueAgentCertificate(resource("agent-p256.csr"), AGENT, AgentRole.SOURCE)
            val leaf = certificate(issued.chainPem)
            assertEquals(T0, leaf.notBefore.toInstant())
            assertEquals(T0 + Duration.ofMinutes(10), leaf.notAfter.toInstant())
        }
    }

    @Test
    fun `Недопустимый срок сертификата агента не даёт серверу стартовать`() {
        for (value in listOf("PT9M59S", "PT0S", "-P1D", "year")) {
            val message = startupFailure("sard.pki.agent-certificate-validity=$value")
            assertTrue("sard.pki.agent-certificate-validity" in message && value in message && "PT10M" in message, message)
        }
    }

    @Test
    fun `Недопустимый интервал продления не даёт серверу стартовать`() {
        for (value in listOf("PT0S", "-PT1H", "hour")) {
            val message = startupFailure("sard.pki.agent-renewal-min-interval=$value")
            assertTrue("sard.pki.agent-renewal-min-interval" in message && value in message, message)
        }
    }
}
