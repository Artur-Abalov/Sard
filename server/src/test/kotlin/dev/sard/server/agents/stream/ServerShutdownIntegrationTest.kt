// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents.stream

import dev.sard.server.TestcontainersConfiguration
import dev.sard.server.enrollment.Enrollment
import dev.sard.server.enrollment.EnrollmentTokens
import dev.sard.server.pki.CertificateAuthority
import io.grpc.Status
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.grpc.test.autoconfigure.LocalGrpcServerPort
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.context.annotation.Import
import org.springframework.grpc.server.lifecycle.GrpcServerLifecycle
import org.springframework.jdbc.core.JdbcTemplate
import org.springframework.test.annotation.DirtiesContext
import java.time.Duration
import kotlin.test.AfterTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

private val GRACE: Duration = Duration.ofSeconds(30)

/**
 * Test 7: stopping the gRPC server (Spring gRPC's lifecycle: GrpcServerShutdownEvent, then
 * `Server.shutdown()` waiting up to the grace period) ends every stream with
 * UNAVAILABLE/SERVER_SHUTTING_DOWN, so agents reconnect, and without waiting out the grace period.
 * The stopped server is not reused: the context is discarded afterwards.
 */
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.RANDOM_PORT,
    properties = [
        "spring.grpc.server.port=0",
        "sard.agent.stream.check-interval=1h",
        "spring.grpc.server.shutdown.grace-period=30s",
    ],
)
@Import(TestcontainersConfiguration::class, StreamTestConfiguration::class)
@DirtiesContext
class ServerShutdownIntegrationTest(
    @Autowired ca: CertificateAuthority,
    @Autowired enrollment: Enrollment,
    @Autowired tokens: EnrollmentTokens,
    @Autowired jdbc: JdbcTemplate,
    @LocalGrpcServerPort port: Int,
    @Autowired private val recorded: RecordingExtensions,
    @Autowired private val server: GrpcServerLifecycle,
) {
    private val clients = StreamClients(ca, enrollment, tokens, jdbc, port)

    @AfterTest
    fun `close the channels`() = clients.closeChannels()

    @Test
    fun `stopping the server closes open streams with SERVER_SHUTTING_DOWN`() {
        clients.createTenant()
        val agents = List(2) { clients.enrolled() }
        val connections =
            agents.map { agent ->
                clients.connect(agent).also {
                    it.hello()
                    recorded.await("${agent.agentId} hello ")
                }
            }

        val started = System.nanoTime()
        server.stop()
        val stopping = Duration.ofNanos(System.nanoTime() - started)

        for (connection in connections) {
            assertEquals(Ended(Status.Code.UNAVAILABLE, "SERVER_SHUTTING_DOWN"), connection.ended())
        }
        assertTrue(stopping < GRACE, "the server waited $stopping for open streams")
    }
}
