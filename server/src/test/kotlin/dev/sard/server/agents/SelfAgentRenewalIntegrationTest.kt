// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents

import dev.sard.server.TestcontainersConfiguration
import dev.sard.server.enrollment.Enrollment
import dev.sard.server.enrollment.EnrollmentTokens
import dev.sard.server.extension.TenantResolver
import dev.sard.server.onboarding.ConfirmedCaStep
import dev.sard.server.pki.CertificateAuthority
import dev.sard.server.pki.MovableClock
import dev.sard.server.pki.PkiFixtures
import dev.sard.server.selfagent.SelfAgentCheck
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.grpc.test.autoconfigure.LocalGrpcServerPort
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.context.annotation.Import
import org.springframework.jdbc.core.JdbcTemplate
import org.springframework.test.context.DynamicPropertyRegistry
import org.springframework.test.context.DynamicPropertySource
import java.nio.file.Files
import java.nio.file.Path
import java.time.Duration
import kotlin.test.AfterTest
import kotlin.test.BeforeTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

private val DEFAULT_TENANT = TenantResolver.DEFAULT_TENANT_ID
private const val SAN_URI = 6

/** C1, rule "Продление выдаёт сертификат той же личности и роли на новый ключ": the built-in agent sard-self. */
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.RANDOM_PORT,
    properties = ["spring.grpc.server.port=0", "sard.self-agent.check-interval=1h", "sard.agent.stream.check-interval=1h"],
)
@Import(TestcontainersConfiguration::class, RenewalClockConfiguration::class, ConfirmedCaStep::class)
class SelfAgentRenewalIntegrationTest(
    @Autowired ca: CertificateAuthority,
    @Autowired private val enrollment: Enrollment,
    @Autowired private val tokens: EnrollmentTokens,
    @Autowired private val check: SelfAgentCheck,
    @Autowired private val jdbc: JdbcTemplate,
    @Autowired private val clock: MovableClock,
    @LocalGrpcServerPort grpcPort: Int,
) {
    private val fixtures = AgentGrpcFixtures(ca, enrollment, tokens, jdbc, grpcPort)
    private val harness = RenewalHarness(fixtures, jdbc, clock)
    private val t0 = RENEWAL_T0

    @BeforeTest
    @AfterTest
    fun clean() {
        clock.now = t0
        jdbc.update("delete from agent_certificates where agent_id in (select id from agents where builtin)")
        jdbc.update("delete from enrollment_tokens where builtin and tenant_id = ?", DEFAULT_TENANT)
        jdbc.update("delete from agents where builtin and tenant_id = ?", DEFAULT_TENANT)
    }

    /** The built-in agent S, enrolled with the built-in token a day before T0. */
    private fun builtinAgent(): EnrolledAgent {
        clock.now = t0 - Duration.ofDays(1)
        val keys = fixtures.newKeys()
        val token = tokens.replaceBuiltin(DEFAULT_TENANT).reveal()
        val enrolled = enrollment.enroll(token, fixtures.csr(keys), "sard-self")
        clock.now = t0
        return EnrolledAgent(
            dev.sard.server.pki
                .AgentIdentity(DEFAULT_TENANT, enrolled.agentId),
            enrolled.certificateChainPem,
            keys,
        )
    }

    private fun builtinTokens() = jdbc.queryForObject("select count(*) from enrollment_tokens where builtin", Int::class.java)

    @Test
    fun `Встроенный агент sard-self продлевает сертификат так же`() {
        val s = builtinAgent()

        val c2 = harness.renew(s)
        harness.confirm(c2)

        assertEquals(s.identity.agentId.toString(), harness.register(c2).agentId)
        val uris =
            PkiFixtures
                .certificate(c2.chainPem)
                .subjectAlternativeNames
                .filter { it[0] == SAN_URI }
                .map { it[1] }
        assertEquals(setOf(s.identity.uri(), "sard://roles/source"), uris.toSet())
        val row = jdbc.queryForMap("select builtin, revoked_at from agents where id = ?", s.identity.agentId)
        assertEquals(true, row["builtin"])
        assertEquals(null, row["revoked_at"])
    }

    @Test
    fun `Продление встроенного агента не выпускает встроенный токен`() {
        val s = builtinAgent()
        harness.confirm(harness.renew(s))
        val before = builtinTokens()
        Files.writeString(channel.resolve("enroll-token"), "left over")

        check.run()

        assertEquals(before, builtinTokens())
        assertFalse(Files.exists(channel.resolve("enroll-token")), "the token file must be gone")
        assertTrue(Files.isDirectory(channel))
    }

    companion object {
        private val channel: Path = Files.createTempDirectory("sard-self-renewal-channel")

        @JvmStatic
        @DynamicPropertySource
        fun properties(registry: DynamicPropertyRegistry) {
            registry.add("sard.self-agent.dir") { channel.toString() }
        }
    }
}
