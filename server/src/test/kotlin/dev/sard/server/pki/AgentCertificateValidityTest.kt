// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.pki

import dev.sard.server.pki.PkiFixtures.AGENT
import dev.sard.server.pki.PkiFixtures.SERVER_NAMES
import dev.sard.server.pki.PkiFixtures.certificate
import dev.sard.server.pki.PkiFixtures.random
import dev.sard.server.pki.PkiFixtures.resource
import dev.sard.server.role.AgentRole
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import org.bouncycastle.asn1.x509.SubjectPublicKeyInfo
import org.junit.jupiter.api.io.TempDir
import java.nio.file.Path
import java.security.cert.X509Certificate
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

    private val keys = Keys.generate(random())

    /** A certificate asked for at T0 from a CA that is valid for [caLeft] more, living [validity] if the CA allows. */
    private fun issued(
        validity: Duration,
        caLeft: Duration = CA_LIFETIME,
    ): X509Certificate {
        val ca = CaKeyPair.generate(MovableClock(T0 - CA_LIFETIME + caLeft), random())
        val key = SubjectPublicKeyInfo.getInstance(keys.public.encoded)
        return MutFlow.underTest { Certificates.agent(key, AGENT, AgentRole.SOURCE, ca, T0, validity, random()) }
    }

    @Test
    fun `Настроенный срок действует на выпуск`() {
        val certificate = issued(Duration.ofMinutes(10))

        assertEquals(T0, certificate.notBefore.toInstant())
        assertEquals(T0 + Duration.ofMinutes(10), certificate.notAfter.toInstant())
        assertEquals(128, certificate.serialNumber.bitLength())
    }

    @Test
    fun `notAfter сертификата агента обрезается по сроку CA`() {
        val certificate = issued(Duration.ofDays(365), caLeft = Duration.ofDays(30))

        assertEquals(T0 + Duration.ofDays(30), certificate.notAfter.toInstant())
    }

    @Test
    fun `Сертификат, укладывающийся в срок CA, не обрезается`() {
        val certificate = issued(Duration.ofDays(29), caLeft = Duration.ofDays(30))

        assertEquals(T0 + Duration.ofDays(29), certificate.notAfter.toInstant())
    }

    @Test
    fun `Срок CA в точности равный сроку сертификата не меняет его`() {
        val certificate = issued(Duration.ofDays(30), caLeft = Duration.ofDays(30))

        assertEquals(T0 + Duration.ofDays(30), certificate.notAfter.toInstant())
    }
}

/** The configured validity reaches the certificate the file CA signs (no mutation: the CSR path is tested elsewhere). */
class FileCertificateAuthorityValidityTest {
    @TempDir
    lateinit var tmp: Path

    @Test
    fun `Срок из настройки применяется к сертификату по времени CA`() {
        val clock = MovableClock(T0)
        val ca =
            FileCertificateAuthority(
                tmp.resolve("pki"),
                SERVER_NAMES,
                clock,
                random(),
                FakeCaLedger(),
                agentValidity = Duration.ofMinutes(10),
            )
        clock.now = T0 + Duration.ofMinutes(7)

        val issued = ca.issueAgentCertificate(resource("agent-p256.csr"), AGENT, AgentRole.SOURCE)

        val leaf = certificate(issued.chainPem)
        assertEquals(T0 + Duration.ofMinutes(7), leaf.notBefore.toInstant())
        assertEquals(T0 + Duration.ofMinutes(17), leaf.notAfter.toInstant())
        assertEquals(leaf.notAfter.toInstant(), issued.notAfter)
    }
}
