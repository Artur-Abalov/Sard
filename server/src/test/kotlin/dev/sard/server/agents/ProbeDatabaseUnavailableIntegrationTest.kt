// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents

import com.google.rpc.ErrorInfo
import dev.sard.proto.agent.v1.AgentServiceGrpc
import dev.sard.proto.agent.v1.ProbeRequest
import dev.sard.server.TestcontainersConfiguration
import dev.sard.server.enrollment.Enrollment
import dev.sard.server.enrollment.EnrollmentTokens
import dev.sard.server.pki.CertificateAuthority
import io.grpc.Status
import io.grpc.StatusRuntimeException
import io.grpc.protobuf.StatusProto
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.grpc.test.autoconfigure.LocalGrpcServerPort
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.context.annotation.Import
import org.springframework.jdbc.core.JdbcTemplate
import org.springframework.test.annotation.DirtiesContext
import org.testcontainers.postgresql.PostgreSQLContainer
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNotNull

/**
 * A8d-3 (ADR 0009 amendment): with the database down Probe is a retryable server fault, not a
 * refusal of the agent and not UNIMPLEMENTED, and the status does not disclose the internal error.
 * [DirtiesContext] retires the context together with its stopped container.
 */
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.RANDOM_PORT,
    properties = ["spring.grpc.server.port=0"],
)
@Import(TestcontainersConfiguration::class)
@DirtiesContext
class ProbeDatabaseUnavailableIntegrationTest(
    @Autowired private val ca: CertificateAuthority,
    @Autowired private val enrollment: Enrollment,
    @Autowired private val tokens: EnrollmentTokens,
    @Autowired private val jdbc: JdbcTemplate,
    @Autowired private val postgres: PostgreSQLContainer,
    @LocalGrpcServerPort private val grpcPort: Int,
) {
    @Test
    fun `an unavailable database gives UNAVAILABLE with INTERNAL_RETRYABLE and no server time`() {
        val fixtures = AgentGrpcFixtures(ca, enrollment, tokens, jdbc, grpcPort)
        val agent = fixtures.enrolled(fixtures.tenant())
        val stub = AgentServiceGrpc.newBlockingStub(fixtures.channel(agent))
        stub.probe(ProbeRequest.getDefaultInstance())

        postgres.stop()

        val outcome = runCatching { stub.probe(ProbeRequest.getDefaultInstance()) }
        val error = outcome.exceptionOrNull() as? StatusRuntimeException
        assertNotNull(error)
        assertEquals(Status.Code.UNAVAILABLE, error.status.code)
        val info = StatusProto.fromStatusAndTrailers(error.status, error.trailers).detailsList.single()
        assertEquals("INTERNAL_RETRYABLE", info.unpack(ErrorInfo::class.java).reason)
        assertFalse("jdbc" in "${error.status.description} ${error.status.cause}".lowercase())
    }
}
