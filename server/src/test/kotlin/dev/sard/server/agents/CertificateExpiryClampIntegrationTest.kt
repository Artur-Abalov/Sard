// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents

import dev.sard.server.TestcontainersConfiguration
import dev.sard.server.enrollment.Enrollment
import dev.sard.server.enrollment.EnrollmentTokens
import dev.sard.server.pki.CertificateAuthority
import dev.sard.server.pki.MovableClock
import dev.sard.server.pki.PkiFixtures
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.grpc.test.autoconfigure.LocalGrpcServerPort
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.boot.test.context.TestConfiguration
import org.springframework.context.annotation.Bean
import org.springframework.context.annotation.Import
import org.springframework.jdbc.core.JdbcTemplate
import java.time.Duration
import kotlin.test.AfterTest
import kotlin.test.BeforeTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

private val CA_LIFETIME = Duration.ofDays(3650)
private val CA_LEFT = Duration.ofDays(30)

/** The CA is created 3620 days before T0, so that it lasts 30 days after T0 (the CA lives 3650 days). */
@TestConfiguration(proxyBeanMethods = false)
class AlmostExpiredCaClockConfiguration {
    @Bean
    fun clock() = MovableClock(RENEWAL_T0 - CA_LIFETIME + CA_LEFT)
}

/** C1, scenario "Обрезанный по CA срок записывается в базу". */
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.RANDOM_PORT,
    properties = ["spring.grpc.server.port=0", "sard.agent.stream.check-interval=1h"],
)
@Import(TestcontainersConfiguration::class, AlmostExpiredCaClockConfiguration::class)
class CertificateExpiryClampIntegrationTest(
    @Autowired private val ca: CertificateAuthority,
    @Autowired enrollment: Enrollment,
    @Autowired tokens: EnrollmentTokens,
    @Autowired private val jdbc: JdbcTemplate,
    @Autowired private val clock: MovableClock,
    @LocalGrpcServerPort grpcPort: Int,
) {
    private val fixtures = AgentGrpcFixtures(ca, enrollment, tokens, jdbc, grpcPort)
    private val harness = RenewalHarness(fixtures, jdbc, clock)
    private val tenant = fixtures.tenant()
    private val t0 = RENEWAL_T0

    @BeforeTest
    fun `the server certificate of the old clock is replaced`() {
        clock.now = t0
        assertTrue(ca.renewServerCertificate())
    }

    @AfterTest
    fun cleanUp() = fixtures.close()

    @Test
    fun `Обрезанный по CA срок записывается в базу`() {
        val x = harness.enrolledAt(tenant, t0 - Duration.ofDays(1))
        clock.now = t0

        val c2 = harness.renew(x)

        val expected = t0 + CA_LEFT
        assertEquals(expected, PkiFixtures.certificate(c2.chainPem).notAfter.toInstant())
        assertEquals(expected, RenewalHarness.toInstant(harness.row(harness.serial(c2))["not_after"]!!))
    }
}
