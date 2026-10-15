// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents

import dev.sard.server.TestcontainersConfiguration
import dev.sard.server.enrollment.Enrollment
import dev.sard.server.enrollment.EnrollmentTokens
import dev.sard.server.pki.CertificateAuthority
import dev.sard.server.pki.MovableClock
import dev.sard.server.pki.PkiFixtures
import dev.sard.server.role.AgentRole
import org.mockito.Mockito
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.grpc.test.autoconfigure.LocalGrpcServerPort
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.context.annotation.Import
import org.springframework.jdbc.core.JdbcTemplate
import org.springframework.test.context.bean.override.mockito.MockitoSpyBean
import java.time.Duration
import java.time.Instant
import kotlin.test.AfterTest
import kotlin.test.Test
import kotlin.test.assertEquals

private const val PORT = "spring.grpc.server.port=0"
private const val QUIET = "sard.agent.stream.check-interval=1h"

/** The server with settings of the certificate lifetime and the renewal interval; the clock starts at the real time. */
abstract class RenewalSettingsTestBase(
    ca: CertificateAuthority,
    enrollment: Enrollment,
    tokens: EnrollmentTokens,
    protected val jdbc: JdbcTemplate,
    protected val clock: MovableClock,
    grpcPort: Int,
) {
    protected val t0: Instant = clock.now
    protected val fixtures = AgentGrpcFixtures(ca, enrollment, tokens, jdbc, grpcPort)
    protected val harness = RenewalHarness(fixtures, jdbc, clock)
    protected val tenant = fixtures.tenant()

    @AfterTest
    fun cleanUp() {
        clock.now = t0
        fixtures.close()
    }
}

/** C1, rule "Срок сертификата агента настраивается", sard.pki.agent-certificate-validity=PT10M. */
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.RANDOM_PORT,
    properties = [PORT, QUIET, "sard.pki.agent-certificate-validity=PT10M"],
)
@Import(TestcontainersConfiguration::class, NowClockConfiguration::class)
class TenMinuteValidityIntegrationTest(
    @Autowired ca: CertificateAuthority,
    @Autowired enrollment: Enrollment,
    @Autowired tokens: EnrollmentTokens,
    @Autowired jdbc: JdbcTemplate,
    @Autowired clock: MovableClock,
    @LocalGrpcServerPort grpcPort: Int,
) : RenewalSettingsTestBase(ca, enrollment, tokens, jdbc, clock, grpcPort) {
    @MockitoSpyBean
    lateinit var authority: CertificateAuthority

    @Test
    fun `Регистрация и продление передают CA настроенный срок`() {
        val x = fixtures.enrolled(tenant)
        clock.now = t0 + Duration.ofMinutes(7)
        harness.renew(x)

        val asked =
            Mockito
                .mockingDetails(authority)
                .invocations
                .filter { it.method.name == "issueAgentCertificate" }
                .map { it.arguments[3] }
        assertEquals(listOf(Duration.ofMinutes(10), Duration.ofMinutes(10)), asked)
    }

    @Test
    fun `Настроенный срок действует на регистрацию`() {
        val x = fixtures.enrolled(tenant)

        assertEquals(t0 + Duration.ofMinutes(10), PkiFixtures.certificate(x.chainPem).notAfter.toInstant())
    }

    @Test
    fun `Настроенный срок действует на продление`() {
        val x = fixtures.enrolled(tenant, AgentRole.SOURCE)
        clock.now = t0 + Duration.ofMinutes(7)

        val c2 = PkiFixtures.certificate(harness.renew(x).chainPem)

        assertEquals(t0 + Duration.ofMinutes(7), c2.notBefore.toInstant())
        assertEquals(t0 + Duration.ofMinutes(17), c2.notAfter.toInstant())
    }

    @Test
    fun `Действующий интервал при сроке PT10M и интервале PT1H это одна минута`() {
        harness.assertIntervalBoundary(tenant, Duration.ofMinutes(1))
    }
}

/** The same, validity PT5H: a tenth of it, 30 minutes, is shorter than the hour of the setting. */
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.RANDOM_PORT,
    properties = [PORT, QUIET, "sard.pki.agent-certificate-validity=PT5H"],
)
@Import(TestcontainersConfiguration::class, NowClockConfiguration::class)
class FiveHourValidityIntegrationTest(
    @Autowired ca: CertificateAuthority,
    @Autowired enrollment: Enrollment,
    @Autowired tokens: EnrollmentTokens,
    @Autowired jdbc: JdbcTemplate,
    @Autowired clock: MovableClock,
    @LocalGrpcServerPort grpcPort: Int,
) : RenewalSettingsTestBase(ca, enrollment, tokens, jdbc, clock, grpcPort) {
    @Test
    fun `Действующий интервал при сроке PT5H и интервале PT1H это 30 минут`() {
        harness.assertIntervalBoundary(tenant, Duration.ofMinutes(30))
    }
}

/** The same, interval PT5M with the default year: the setting is the shorter. */
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.RANDOM_PORT,
    properties = [PORT, QUIET, "sard.pki.agent-renewal-min-interval=PT5M"],
)
@Import(TestcontainersConfiguration::class, NowClockConfiguration::class)
class FiveMinuteIntervalIntegrationTest(
    @Autowired ca: CertificateAuthority,
    @Autowired enrollment: Enrollment,
    @Autowired tokens: EnrollmentTokens,
    @Autowired jdbc: JdbcTemplate,
    @Autowired clock: MovableClock,
    @LocalGrpcServerPort grpcPort: Int,
) : RenewalSettingsTestBase(ca, enrollment, tokens, jdbc, clock, grpcPort) {
    @Test
    fun `Действующий интервал при сроке P365D и интервале PT5M это 5 минут`() {
        harness.assertIntervalBoundary(tenant, Duration.ofMinutes(5))
    }
}
