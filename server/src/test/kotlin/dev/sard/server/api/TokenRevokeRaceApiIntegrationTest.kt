// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import dev.sard.server.TestcontainersConfiguration
import dev.sard.server.enrollment.Enrollment
import dev.sard.server.enrollment.EnrollmentTestConfiguration
import dev.sard.server.enrollment.GatedCertificateAuthority
import dev.sard.server.enrollment.awaitEnrollmentRowLockWait
import dev.sard.server.pki.PkiFixtures
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.boot.test.web.server.LocalServerPort
import org.springframework.context.annotation.Import
import org.springframework.jdbc.core.JdbcTemplate
import tools.jackson.databind.ObjectMapper
import java.time.Duration
import java.util.concurrent.Callable
import java.util.concurrent.CountDownLatch
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit
import kotlin.test.AfterTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

private const val WAIT_SECONDS = 20L

/** Rule "Токены регистрации": a revoke that loses the race with a registration answers 409 token_used. */
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.RANDOM_PORT,
    properties = ["spring.grpc.server.port=0"],
)
@Import(TestcontainersConfiguration::class, EnrollmentTestConfiguration::class, WizardCodeConfiguration::class)
class TokenRevokeRaceApiIntegrationTest(
    @Autowired private val enrollment: Enrollment,
    @Autowired private val ca: GatedCertificateAuthority,
    @Autowired private val jdbc: JdbcTemplate,
    @Autowired mapper: ObjectMapper,
    @LocalServerPort port: Int,
) {
    private val api = ApiClient(port, mapper)
    private val admin = api.signIn()
    private val csr = PkiFixtures.resource("agent-p256.csr")

    @AfterTest
    fun `the default tenant is left clean`() {
        ca.gate = null
        for (table in listOf("agent_certificates", "enrollment_tokens", "agents")) {
            jdbc.update("delete from $table")
        }
    }

    @Test
    fun `Отзыв, проигравший гонку регистрации, отвечает 409 token_used`() {
        val created = api.post("/api/v1/enrollment-tokens", admin).json
        ca.gate = CountDownLatch(1)
        val pool = Executors.newFixedThreadPool(2)
        try {
            val registration = pool.submit(Callable { enrollment.enroll(created.path("token").asString(), csr, "db1") })
            assertTrue(ca.entered.tryAcquire(WAIT_SECONDS, TimeUnit.SECONDS), "the registration never reached the CA")
            val revocation =
                pool.submit(
                    Callable {
                        api.post(
                            "/api/v1/enrollment-tokens/${created.path("id").asString()}/revoke",
                            admin,
                            null,
                        )
                    },
                )
            jdbc.awaitEnrollmentRowLockWait(Duration.ofSeconds(WAIT_SECONDS))
            ca.gate?.countDown()

            val agent = registration.get(WAIT_SECONDS, TimeUnit.SECONDS)
            val response = revocation.get(WAIT_SECONDS, TimeUnit.SECONDS)

            assertEquals(409, response.status)
            assertEquals("token_used", response.code)
            assertEquals(agent.agentId.toString(), response.json.path("agentId").asString())
        } finally {
            ca.gate?.countDown()
            pool.shutdownNow()
        }
    }
}
