// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents

import com.google.rpc.ErrorInfo
import dev.sard.proto.agent.v1.AgentServiceGrpc
import dev.sard.server.TestcontainersConfiguration
import dev.sard.server.api.MACHINE_ID
import dev.sard.server.api.OTHER_MACHINE_ID
import dev.sard.server.enrollment.Enrollment
import dev.sard.server.enrollment.EnrollmentTokens
import dev.sard.server.persistence.TenantSessions
import dev.sard.server.pki.CertificateAuthority
import io.grpc.Status
import io.grpc.StatusRuntimeException
import io.grpc.protobuf.StatusProto
import org.hibernate.Session
import org.hibernate.exception.JDBCConnectionException
import org.mockito.ArgumentMatchers
import org.mockito.Mockito
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.grpc.test.autoconfigure.LocalGrpcServerPort
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.context.annotation.Import
import org.springframework.jdbc.core.JdbcTemplate
import org.springframework.test.context.bean.override.mockito.MockitoSpyBean
import java.sql.SQLException
import java.util.UUID
import java.util.concurrent.TimeUnit
import kotlin.test.AfterTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith

/** V1: the database is down while Register stores the machine id. */
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.RANDOM_PORT,
    properties = ["spring.grpc.server.port=0"],
)
@Import(TestcontainersConfiguration::class)
class RegisterMachineIdUnavailableIntegrationTest(
    @Autowired ca: CertificateAuthority,
    @Autowired enrollment: Enrollment,
    @Autowired tokens: EnrollmentTokens,
    @Autowired private val jdbc: JdbcTemplate,
    @LocalGrpcServerPort grpcPort: Int,
) {
    @MockitoSpyBean
    lateinit var sessions: TenantSessions

    private val fixtures = AgentGrpcFixtures(ca, enrollment, tokens, jdbc, grpcPort)

    @AfterTest
    fun cleanUp() {
        Mockito.reset(sessions)
        fixtures.close()
    }

    private fun <T : Any> anything(placeholder: T): T {
        ArgumentMatchers.any<Any>()
        return placeholder
    }

    private fun machineIdOf(agent: EnrolledAgent) =
        jdbc.queryForObject("select machine_id from agents where id = ?", String::class.java, agent.identity.agentId)

    @Test
    fun `Недоступная база при сохранении machine_id даёт INTERNAL_RETRYABLE`() {
        val agent = fixtures.enrolled(fixtures.tenant())
        val stub = AgentServiceGrpc.newBlockingStub(fixtures.channel(agent)).withDeadlineAfter(10, TimeUnit.SECONDS)
        stub.register(fixtures.registerRequest(machineId = MACHINE_ID).build())
        val failure = JDBCConnectionException("down", SQLException("down"))
        Mockito
            .doThrow(failure)
            .`when`(sessions)
            .inTenant(anything(UUID.randomUUID()), anything<(Session) -> Any?> { null })

        val error =
            assertFailsWith<StatusRuntimeException> {
                stub.register(fixtures.registerRequest(machineId = OTHER_MACHINE_ID).build())
            }

        val info =
            StatusProto
                .fromThrowable(error)!!
                .detailsList
                .single()
                .unpack(ErrorInfo::class.java)
        assertEquals(Status.Code.UNAVAILABLE to "INTERNAL_RETRYABLE", error.status.code to info.reason)
        Mockito.reset(sessions)
        assertEquals(MACHINE_ID, machineIdOf(agent))
    }
}
