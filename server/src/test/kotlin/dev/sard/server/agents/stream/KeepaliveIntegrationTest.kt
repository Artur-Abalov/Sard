// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents.stream

import dev.sard.server.TestcontainersConfiguration
import dev.sard.server.enrollment.Enrollment
import dev.sard.server.enrollment.EnrollmentTokens
import dev.sard.server.pki.CertificateAuthority
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.grpc.test.autoconfigure.LocalGrpcServerPort
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.context.annotation.Import
import org.springframework.jdbc.core.JdbcTemplate
import java.time.Duration
import java.util.concurrent.TimeUnit
import kotlin.test.AfterTest
import kotlin.test.BeforeTest
import kotlin.test.Test
import kotlin.test.assertEquals

/** grpc-java clamps a client's keepalive time to at least 10 s. */
private const val CLIENT_PING_SECONDS = 10L

/**
 * Test 1: a client pinging on an idle stream is not cut off. Production: the agent pings every
 * 30 s, the server permits 20 s. grpc-java clients cannot ping faster than every 10 s, so this
 * keeps the proportion with 10 s against 6 s and waits in real time — it is the transport's
 * clock, not ours. Without the permit (grpc-java default 5 min) the third ping, at 30 s, draws
 * GOAWAY too_many_pings: a wait of 40 s covers it with a margin (control run: session log).
 */
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.RANDOM_PORT,
    properties = [
        "spring.grpc.server.port=0",
        "sard.agent.stream.check-interval=1h",
        "spring.grpc.server.keepalive.permit.time=6s",
    ],
)
@Import(TestcontainersConfiguration::class, StreamTestConfiguration::class)
class KeepaliveIntegrationTest(
    @Autowired ca: CertificateAuthority,
    @Autowired enrollment: Enrollment,
    @Autowired tokens: EnrollmentTokens,
    @Autowired jdbc: JdbcTemplate,
    @LocalGrpcServerPort port: Int,
    @Autowired private val recorded: RecordingExtensions,
) {
    private val clients = StreamClients(ca, enrollment, tokens, jdbc, port)

    @BeforeTest
    fun `a fresh tenant`() = clients.createTenant()

    @AfterTest
    fun `drop the tenant`() = clients.close()

    @Test
    fun `a client pinging an idle stream within the permitted rate stays connected`() {
        val agent = clients.enrolled()
        val connection =
            clients.connect(agent) {
                it.keepAliveTime(CLIENT_PING_SECONDS, TimeUnit.SECONDS)
                it.keepAliveTimeout(CLIENT_PING_SECONDS / 2, TimeUnit.SECONDS)
            }
        connection.hello()
        recorded.await("${agent.agentId} hello ")

        assertEquals(null, connection.endedWithin(Duration.ofSeconds(4 * CLIENT_PING_SECONDS)))
        connection.progress("still-here")
        recorded.await("${agent.agentId} progress still-here")
    }
}
