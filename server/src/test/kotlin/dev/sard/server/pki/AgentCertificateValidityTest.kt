// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.pki

import dev.sard.server.pki.PkiFixtures.AGENT
import dev.sard.server.pki.PkiFixtures.certificate
import dev.sard.server.pki.PkiFixtures.random
import dev.sard.server.pki.PkiFixtures.resource
import dev.sard.server.role.AgentRole
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import org.junit.jupiter.api.io.TempDir
import java.nio.file.Path
import java.time.Duration
import java.time.Instant
import kotlin.test.Test
import kotlin.test.assertEquals

private val T0 = Instant.parse("2026-10-15T12:00:00Z")
private val CA_LIFETIME = Duration.ofDays(3650)

/** C1, rule "Срок сертификата агента настраивается и не выходит за срок CA": the CA side. */
@MutFlowTest
class AgentCertificateValidityTest {
    @TempDir
    lateinit var tmp: Path

    /** A CA that is valid until [caLeft] after T0, asked at T0 for a certificate living [validity]. */
    private fun issuedNotAfter(
        validity: Duration,
        caLeft: Duration = CA_LIFETIME,
    ): Instant {
        val clock = MovableClock(T0 - CA_LIFETIME + caLeft)
        val ca =
            FileCertificateAuthority(
                tmp.resolve("pki"),
                PkiFixtures.SERVER_NAMES,
                clock,
                random(),
                FakeCaLedger(),
                agentValidity = validity,
            )
        clock.now = T0
        val issued = MutFlow.underTest { ca.issueAgentCertificate(resource("agent-p256.csr"), AGENT, AgentRole.SOURCE) }
        val leaf = certificate(issued.chainPem)
        assertEquals(T0, leaf.notBefore.toInstant())
        assertEquals(leaf.notAfter.toInstant(), issued.notAfter)
        return issued.notAfter
    }

    @Test
    fun `Настроенный срок действует на выпуск`() {
        assertEquals(T0 + Duration.ofMinutes(10), issuedNotAfter(Duration.ofMinutes(10)))
    }

    @Test
    fun `notAfter сертификата агента обрезается по сроку CA`() {
        assertEquals(T0 + Duration.ofDays(30), issuedNotAfter(Duration.ofDays(365), caLeft = Duration.ofDays(30)))
    }

    @Test
    fun `Сертификат, укладывающийся в срок CA, не обрезается`() {
        assertEquals(T0 + Duration.ofDays(29), issuedNotAfter(Duration.ofDays(29), caLeft = Duration.ofDays(30)))
    }
}
