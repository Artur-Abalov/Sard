// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents

import dev.sard.server.TestcontainersConfiguration
import dev.sard.server.enrollment.Enrollment
import dev.sard.server.enrollment.EnrollmentTokens
import dev.sard.server.fleet.Agents
import dev.sard.server.pki.AgentIdentity
import dev.sard.server.pki.CertificateAuthority
import dev.sard.server.pki.FakeCaLedger
import dev.sard.server.pki.FileCertificateAuthority
import dev.sard.server.pki.ForgedCertificates
import dev.sard.server.pki.ForgedRole
import dev.sard.server.pki.MovableClock
import dev.sard.server.pki.Pem
import dev.sard.server.role.AgentRole
import io.grpc.Status
import org.junit.jupiter.api.io.TempDir
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.grpc.test.autoconfigure.LocalGrpcServerPort
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.context.annotation.Import
import org.springframework.jdbc.core.JdbcTemplate
import java.math.BigInteger
import java.nio.file.Path
import java.security.SecureRandom
import java.sql.Timestamp
import java.time.Clock
import java.time.Duration
import kotlin.test.AfterTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNull

private val DAY = Duration.ofDays(1)

/** C1, rule "Отозванный агент и чужой сертификат не продлеваются". */
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.RANDOM_PORT,
    properties = ["spring.grpc.server.port=0", "sard.agent.stream.check-interval=1h"],
)
@Import(TestcontainersConfiguration::class, RenewalClockConfiguration::class)
class CertificateRenewalRefusalsIntegrationTest(
    @Autowired private val ca: CertificateAuthority,
    @Autowired enrollment: Enrollment,
    @Autowired tokens: EnrollmentTokens,
    @Autowired private val agents: Agents,
    @Autowired private val jdbc: JdbcTemplate,
    @Autowired private val clock: MovableClock,
    @LocalGrpcServerPort grpcPort: Int,
) {
    @TempDir
    lateinit var tmp: Path

    private val fixtures = AgentGrpcFixtures(ca, enrollment, tokens, jdbc, grpcPort)
    private val harness = RenewalHarness(fixtures, jdbc, clock)
    private val tenant = fixtures.tenant()
    private val t0 = RENEWAL_T0

    @AfterTest
    fun cleanUp() {
        clock.now = t0
        fixtures.close()
    }

    private fun registeredX(): EnrolledAgent {
        val x = harness.enrolledAt(tenant, t0 - DAY)
        clock.now = t0
        return x
    }

    private fun revokeByAdministrator(agent: EnrolledAgent) {
        agents.revoke(tenant, agent.identity.agentId, confirmation = null)
    }

    private fun assertRenewalRefused(
        reason: String,
        state: (EnrolledAgent) -> Unit,
    ) {
        val x = registeredX()
        state(x)
        val before = harness.count(x)
        harness.assertRefusedBy(reason) { harness.renew(x) }
        harness.assertNoNewCertificates(x, before)
    }

    // --- Отозванный или истёкший сертификат не продлевается

    @Test
    fun `Администратор отозвал агента, продление отклонено как CERT_REVOKED`() =
        assertRenewalRefused("CERT_REVOKED") { revokeByAdministrator(it) }

    @Test
    fun `Запись сертификата отозвана, продление отклонено как CERT_REVOKED`() =
        assertRenewalRefused("CERT_REVOKED") {
            val sql = "update agent_certificates set revoked_at = ? where serial = ?"
            jdbc.update(sql, Timestamp.from(t0), harness.serial(it))
        }

    @Test
    fun `Наступил not_after, продление отклонено как CERT_EXPIRED`() =
        assertRenewalRefused("CERT_EXPIRED") {
            clock.now = RenewalHarness.toInstant(harness.row(harness.serial(it))["not_after"]!!)
        }

    @Test
    fun `Агент отозван правкой базы без отзыва сертификата, продление отклонено как AGENT_REVOKED`() =
        assertRenewalRefused("AGENT_REVOKED") {
            jdbc.update("update agents set revoked_at = ? where id = ?", Timestamp.from(t0), it.identity.agentId)
        }

    @Test
    fun `Отзыв агента отзывает и неподтверждённый сертификат`() {
        val c1 = registeredX()
        val c2 = harness.renew(c1)
        revokeByAdministrator(c1)

        harness.assertRefusedBy("CERT_REVOKED") { harness.confirm(c2) }

        assertNull(harness.supersededAt(harness.serial(c1)))
    }

    @Test
    fun `Отзыв после продления отзывает новый сертификат`() {
        val c1 = registeredX()
        val c2 = harness.renew(c1)
        harness.confirm(c2)
        revokeByAdministrator(c1)

        harness.assertRefusedBy("CERT_REVOKED") { harness.register(c2) }
    }

    // --- Чужой сертификат не продлевается

    private fun assertForeignRefused(
        reason: String?,
        foreign: (x: EnrolledAgent, y: EnrolledAgent) -> EnrolledAgent,
    ) {
        val x = registeredX()
        val y = fixtures.enrolled(fixtures.tenant(), AgentRole.VERIFIER)
        val (beforeX, beforeY) = harness.count(x) to harness.count(y)
        val presented = foreign(x, y)
        if (reason == null) {
            assertEquals(Status.Code.UNAVAILABLE, harness.refused { harness.renew(presented) }.code)
        } else {
            harness.assertRefusedBy(reason) { harness.renew(presented) }
        }
        harness.assertNoNewCertificates(x, beforeX)
        harness.assertNoNewCertificates(y, beforeY)
    }

    @Test
    fun `Сертификат другого CA с URI личности X не проходит рукопожатие TLS`() =
        assertForeignRefused(null) { x, _ ->
            val stranger =
                FileCertificateAuthority(
                    tmp.resolve("pki"),
                    listOf("localhost"),
                    Clock.systemUTC(),
                    SecureRandom(),
                    FakeCaLedger(permissive = true),
                )
            val keys = fixtures.newKeys()
            val issued = stranger.issueAgentCertificate(fixtures.csr(keys), x.identity, AgentRole.SOURCE, DAY)
            EnrolledAgent(x.identity, issued.chainPem, keys)
        }

    @Test
    fun `Сертификат CA сервера с неизвестным серийным номером это CERT_UNKNOWN`() =
        assertForeignRefused("CERT_UNKNOWN") { x, _ ->
            val keys = fixtures.newKeys()
            val serial = BigInteger(127, SecureRandom()).setBit(127)
            val forged = ForgedCertificates.of(ca).forge(keys, serial, x.identity, ForgedRole.Text("source"))
            EnrolledAgent(x.identity, Pem.certificate(forged), keys)
        }

    @Test
    fun `Сертификат CA сервера с серийным номером C1 и URI личности Y это CERT_IDENTITY_MISMATCH`() =
        assertForeignRefused("CERT_IDENTITY_MISMATCH") { x, y ->
            fixtures.forged(x, ForgedRole.Text("source"), identity = y.identity)
        }

    @Test
    fun `Сертификат CA сервера с серийным номером C1 и URI роли verifier это CERT_IDENTITY_MISMATCH`() =
        assertForeignRefused("CERT_IDENTITY_MISMATCH") { x, _ -> fixtures.forged(x, ForgedRole.Text("verifier")) }
}
