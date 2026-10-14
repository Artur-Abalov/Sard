// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.enrollment

import com.google.rpc.ErrorInfo
import dev.sard.proto.agent.v1.EnrollRequest
import dev.sard.proto.agent.v1.EnrollmentServiceGrpcKt
import dev.sard.server.TestcontainersConfiguration
import dev.sard.server.completeOnboarding
import dev.sard.server.pki.MovableClock
import dev.sard.server.pki.PkiFixtures.resource
import dev.sard.server.role.AgentRole
import io.grpc.Grpc
import io.grpc.Status
import io.grpc.TlsChannelCredentials
import io.grpc.protobuf.StatusProto
import kotlinx.coroutines.runBlocking
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.grpc.test.autoconfigure.LocalGrpcServerPort
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.context.annotation.Import
import org.springframework.jdbc.core.JdbcTemplate
import java.util.UUID
import kotlin.test.AfterTest
import kotlin.test.BeforeTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNull
import kotlin.test.assertTrue

/** Rule "Регистрация новых агентов закрыта до завершения онбординга" of the F4b server specification. */
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.RANDOM_PORT,
    properties = ["spring.grpc.server.port=0"],
)
@Import(TestcontainersConfiguration::class, EnrollmentTestConfiguration::class)
class EnrollmentOnboardingIntegrationTest(
    @Autowired private val tokens: EnrollmentTokens,
    @Autowired private val ca: GatedCertificateAuthority,
    @Autowired private val clock: MovableClock,
    @Autowired private val jdbc: JdbcTemplate,
    @LocalGrpcServerPort private val grpcPort: Int,
) {
    private val acme = UUID.randomUUID()
    private val csr = resource("agent-p256.csr")

    @BeforeTest
    fun `an onboarding that is not complete and a fresh tenant`() {
        clock.now = ENROLLMENT_NOW
        ca.gate = null
        jdbc.insertTenant(acme)
        jdbc.update("delete from onboarding_steps where step = 'keys_confirmed'")
    }

    @AfterTest
    fun `complete the onboarding again and drop the tenant`() {
        completeOnboarding(jdbc)
        jdbc.deleteEnrollmentTenantData(acme)
    }

    private fun enroll(token: String): Result<dev.sard.proto.agent.v1.EnrollResponse> {
        val request: EnrollRequest = enrollRequest(token, csr)
        val trust = TlsChannelCredentials.newBuilder().trustManager(ca.caBundlePem().byteInputStream()).build()
        val channel = Grpc.newChannelBuilderForAddress("localhost", grpcPort, trust).build()
        return try {
            val stub = EnrollmentServiceGrpcKt.EnrollmentServiceCoroutineStub(channel)
            runCatching { runBlocking { stub.enroll(request) } }
        } finally {
            channel.shutdownNow()
        }
    }

    private fun tokenUsedAt(id: UUID): Any? {
        val sql = "select used_at, agent_id from enrollment_tokens where id = ?"
        return jdbc.queryForMap(sql, id)["used_at"]
    }

    private fun agents() = jdbc.countAgents(acme)

    @Test
    fun `Регистрация обычным токеном до завершения онбординга отклоняется`() {
        val issued = tokens.create(acme, ENROLLMENT_TOKEN_TTL, role = AgentRole.SOURCE)

        val failure = enroll(issued.reveal()).exceptionOrNull()

        val status = checkNotNull(StatusProto.fromThrowable(failure))
        assertEquals(Status.Code.FAILED_PRECONDITION.value(), status.code)
        val info = status.detailsList.single { it.`is`(ErrorInfo::class.java) }.unpack(ErrorInfo::class.java)
        assertEquals("ONBOARDING_INCOMPLETE", info.reason)
        assertEquals("sard.dev", info.domain)
        assertNull(tokenUsedAt(issued.id))
        assertEquals(0, agents())
    }

    @Test
    fun `После завершения онбординга тот же токен регистрирует агента`() {
        val issued = tokens.create(acme, ENROLLMENT_TOKEN_TTL, role = AgentRole.SOURCE)
        assertTrue(enroll(issued.reveal()).isFailure)

        completeOnboarding(jdbc)
        val response = enroll(issued.reveal()).getOrThrow()

        assertTrue(response.agentId.isNotEmpty())
        assertTrue(response.certificateChainPem.isNotEmpty())
        assertTrue(tokenUsedAt(issued.id) != null)
    }

    @Test
    fun `Встроенный токен регистрирует sard-self до завершения онбординга`() {
        val issued = tokens.replaceBuiltin(acme)

        val response = enroll(issued.reveal()).getOrThrow()

        assertTrue(response.agentId.isNotEmpty())
        val sql = "select builtin from agents where id = ?::uuid"
        val builtin = jdbc.queryForObject(sql, Boolean::class.java, response.agentId)
        assertEquals(true, builtin)
    }
}
