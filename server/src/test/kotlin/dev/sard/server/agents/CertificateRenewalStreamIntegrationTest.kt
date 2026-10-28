// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents

import dev.sard.proto.agent.v1.BackupOutput
import dev.sard.proto.agent.v1.ConnectResponse
import dev.sard.proto.agent.v1.StepResult
import dev.sard.proto.agent.v1.StepStatus
import dev.sard.server.TestcontainersConfiguration
import dev.sard.server.agents.dispatch.DispatchTestConfiguration
import dev.sard.server.agents.dispatch.ReconciledHellos
import dev.sard.server.agents.stream.AgentConnections
import dev.sard.server.agents.stream.AgentSessionRegistry
import dev.sard.server.agents.stream.Connection
import dev.sard.server.agents.stream.Ended
import dev.sard.server.agents.stream.StreamClients
import dev.sard.server.agents.stream.TestAgent
import dev.sard.server.agents.stream.WAIT_SECONDS
import dev.sard.server.enrollment.Enrollment
import dev.sard.server.enrollment.EnrollmentTokens
import dev.sard.server.fleet.Agents
import dev.sard.server.pki.CertificateAuthority
import dev.sard.server.pki.MovableClock
import dev.sard.server.runs.Runs
import dev.sard.server.runs.SourceDraft
import dev.sard.server.runs.Sources
import io.grpc.Status
import io.micrometer.core.instrument.MeterRegistry
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.grpc.test.autoconfigure.LocalGrpcServerPort
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.context.annotation.Import
import org.springframework.jdbc.core.JdbcTemplate
import java.sql.Timestamp
import java.time.Duration
import java.time.Instant
import java.util.UUID
import java.util.concurrent.TimeUnit
import kotlin.test.AfterTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNull
import kotlin.test.assertTrue

private const val REPOSITORY_ID = "5f0c3e2d9a8b7c6d5e4f3a2b1c0d9e8f7a6b5c4d3e2f1a0b9c8d7e6f5a4b3c2d"
private const val POLL_MILLIS = 20L

/** C1, rule "Открытый поток не рвётся вытеснением", over the real server: TLS, PostgreSQL, Connect streams. */
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.RANDOM_PORT,
    properties = ["spring.grpc.server.port=0", "sard.agent.stream.check-interval=1h"],
)
@Import(TestcontainersConfiguration::class, DispatchTestConfiguration::class)
class CertificateRenewalStreamIntegrationTest(
    @Autowired private val ca: CertificateAuthority,
    @Autowired enrollment: Enrollment,
    @Autowired tokens: EnrollmentTokens,
    @Autowired private val agents: Agents,
    @Autowired private val hellos: ReconciledHellos,
    @Autowired private val connections: AgentConnections,
    @Autowired private val registry: AgentSessionRegistry,
    @Autowired private val sources: Sources,
    @Autowired private val runs: Runs,
    @Autowired private val meters: MeterRegistry,
    @Autowired private val jdbc: JdbcTemplate,
    @Autowired private val clock: MovableClock,
    @LocalGrpcServerPort private val port: Int,
) {
    private val fixtures = AgentGrpcFixtures(ca, enrollment, tokens, jdbc, port)
    private val harness = RenewalHarness(fixtures, jdbc, clock)
    private val clients = StreamClients(ca, enrollment, tokens, jdbc, port)
    private val tenant = fixtures.tenant()
    private val start = clock.now

    @AfterTest
    fun cleanUp() {
        clock.now = start
        clients.closeChannels()
        for (table in listOf("no_verifier_episodes", "snapshots", "step_logs", "run_steps", "runs", "sources")) {
            jdbc.update("delete from $table where tenant_id = ?", tenant)
        }
        fixtures.close()
    }

    /** An enrolled source with the plugin and repository a run needs. */
    private fun enrolled(): EnrolledAgent {
        val agent = harness.enrolledAt(tenant, start - Duration.ofDays(1))
        clock.now = start
        jdbc.update(
            """
            insert into agent_plugins (tenant_id, agent_id, name, version, config_schema, actions)
            values (?, ?, 'postgresql', '0.1.0', '{}'::jsonb, array['backup'])
            """.trimIndent(),
            tenant,
            agent.identity.agentId,
        )
        jdbc.update(
            "insert into agent_repositories (tenant_id, agent_id, name, backend) values (?, ?, 'main', 'local')",
            tenant,
            agent.identity.agentId,
        )
        return agent
    }

    private fun testAgent(agent: EnrolledAgent) = TestAgent(tenant, agent.identity.agentId, fixtures.presenting(agent))

    private fun greeted(agent: EnrolledAgent): Connection =
        clients.connect(testAgent(agent)).also {
            it.hello()
            hellos.await(agent.identity.agentId)
        }

    /** Renewed and confirmed on separate connections while [stream] stays open. */
    private fun renewedAndConfirmed(agent: EnrolledAgent): EnrolledAgent {
        val renewed = harness.renew(agent)
        harness.confirm(renewed)
        return renewed
    }

    private fun supersede(agent: EnrolledAgent) {
        val sql = "update agent_certificates set superseded_at = ? where serial = ?"
        jdbc.update(sql, Timestamp.from(clock.now), harness.serial(agent))
    }

    private fun notAfter(agent: EnrolledAgent): Instant {
        val recorded = harness.row(harness.serial(agent))["not_after"]!!
        return RenewalHarness.toInstant(recorded)
    }

    private fun duplicates() = meters.counter("sard.agent.duplicate.sessions").count()

    private fun duplicateMark(agent: EnrolledAgent) =
        jdbc.queryForObject(
            "select duplicate_session_at from agents where id = ?",
            Timestamp::class.java,
            agent.identity.agentId,
        )

    private fun dispatchedStep(agent: EnrolledAgent): UUID {
        val draft = SourceDraft("prod-db", agent.identity.agentId, "postgresql", "main", """{"database": "app"}""")
        return runs
            .start(tenant, sources.create(tenant, draft).id)
            .steps
            .single()
            .id
    }

    private fun Connection.nextOfKind(kind: ConnectResponse.MessageCase): ConnectResponse {
        while (true) {
            val message = checkNotNull(received.poll(WAIT_SECONDS, TimeUnit.SECONDS)) { "no $kind" }
            if (message.messageCase == kind) return message
        }
    }

    private fun succeeded(step: UUID) =
        StepResult
            .newBuilder()
            .setCommandId(step.toString())
            .setStatus(StepStatus.STEP_STATUS_SUCCEEDED)
            .setBackup(
                BackupOutput
                    .newBuilder()
                    .setSnapshotId("4a3b2c1d")
                    .setTotalBytes(1_000)
                    .setAddedBytes(100)
                    .setRepositoryId(REPOSITORY_ID),
            ).build()

    private fun leave(agent: EnrolledAgent) {
        clients.closeChannels()
        val deadline = System.nanoTime() + Duration.ofSeconds(WAIT_SECONDS).toNanos()
        while (registry.session(agent.identity.agentId) != null) {
            check(System.nanoTime() < deadline) { "the server kept the session of ${agent.identity.agentId}" }
            Thread.sleep(POLL_MILLIS)
        }
    }

    @Test
    fun `Поток со старым сертификатом переживает подтверждение нового`() {
        val c1 = enrolled()
        val stream = greeted(c1)
        renewedAndConfirmed(c1)

        connections.check()

        assertTrue(stream.isOpen)
        val step = dispatchedStep(c1)
        assertEquals(step.toString(), stream.nextOfKind(ConnectResponse.MessageCase.RUN_STEP).runStep.commandId)
        stream.result(succeeded(step))
        assertEquals(step.toString(), stream.nextOfKind(ConnectResponse.MessageCase.RESULT_ACK).resultAck.commandId)
        val status = jdbc.queryForObject("select status from run_steps where id = ?", String::class.java, step)
        assertEquals("succeeded", status)
    }

    @Test
    fun `Унарные вызовы по новому сертификату не считаются вторым сеансом`() {
        val c1 = enrolled()
        val stream = greeted(c1)
        stream.heartbeat(clock.now)
        val before = duplicates()

        renewedAndConfirmed(c1)
        connections.check()

        assertTrue(stream.isOpen)
        assertNull(duplicateMark(c1))
        assertEquals(before, duplicates())
    }

    @Test
    fun `Переподключение с новым сертификатом после конца старого потока принимается`() {
        val c1 = enrolled()
        greeted(c1)
        val c2 = renewedAndConfirmed(c1)
        leave(c1)

        val response = harness.register(c2)
        val stream = greeted(c2)

        assertEquals(c2.identity.agentId.toString(), response.agentId)
        assertTrue(stream.isOpen)
        assertTrue(registry.online(c2.identity.agentId))
        assertNull(duplicateMark(c2))
    }

    @Test
    fun `Поток со старым сертификатом закрывается при отзыве агента`() {
        val c1 = enrolled()
        val stream = greeted(c1)
        renewedAndConfirmed(c1)
        // The real revocation path closes the stream at once, without waiting for the periodic check.
        agents.revoke(tenant, c1.identity.agentId, confirmation = null)

        assertEquals(Ended(Status.Code.UNAUTHENTICATED, "AGENT_REVOKED"), stream.ended())
    }

    @Test
    fun `Поток со старым сертификатом закрывается при истечении его срока`() {
        val c1 = enrolled()
        val stream = greeted(c1)
        renewedAndConfirmed(c1)
        clock.now = notAfter(c1)

        connections.check()

        assertEquals(Ended(Status.Code.UNAUTHENTICATED, "CERT_EXPIRED"), stream.ended())
    }

    @Test
    fun `Агент без продления работает со своим сертификатом до его срока`() {
        val c1 = enrolled()
        clock.now = start - Duration.ofDays(1) + Duration.ofDays(364)

        val response = harness.register(c1)
        val stream = greeted(c1)

        assertEquals(c1.identity.agentId.toString(), response.agentId)
        assertTrue(stream.isOpen)
        assertTrue(registry.online(c1.identity.agentId))
    }

    // --- Сверка открытых потоков (S5a)

    @Test
    fun `Сверка открытых потоков не закрывает поток из-за вытеснения`() {
        val c = enrolled()
        val stream = greeted(c)
        supersede(c)

        connections.check()

        assertTrue(stream.isOpen)
        assertTrue(registry.online(c.identity.agentId))
        harness.assertRefusedBy("CERT_SUPERSEDED") { harness.register(c) }
    }

    @Test
    fun `Сверка открытых потоков не закрывает поток из-за неподтверждённости`() {
        val c = enrolled()
        val stream = greeted(c)
        val sibling = "5".repeat(32)
        jdbc.update(
            "insert into agent_certificates (serial, tenant_id, agent_id, issued_at, not_after) values (?, ?, ?, ?, ?)",
            sibling,
            tenant,
            c.identity.agentId,
            Timestamp.from(start),
            Timestamp.from(start.plus(Duration.ofDays(1))),
        )
        jdbc.update("update agent_certificates set predecessor_serial = ? where serial = ?", sibling, harness.serial(c))

        connections.check()

        assertTrue(stream.isOpen)
        harness.assertRefusedBy("CERT_NOT_CONFIRMED") { harness.register(c) }
    }

    private fun supersededWith(
        event: (EnrolledAgent) -> Unit,
        reason: String,
    ) {
        val c = enrolled()
        val stream = greeted(c)
        supersede(c)
        event(c)

        connections.check()

        assertEquals(Ended(Status.Code.UNAUTHENTICATED, reason), stream.ended())
    }

    @Test
    fun `Сверка закрывает поток вытесненного сертификата при отзыве записи`() =
        supersededWith({
            val sql = "update agent_certificates set revoked_at = ? where serial = ?"
            jdbc.update(sql, Timestamp.from(clock.now), harness.serial(it))
        }, "CERT_REVOKED")

    @Test
    fun `Сверка закрывает поток вытесненного сертификата при отзыве агента правкой базы`() =
        supersededWith({
            jdbc.update("update agents set revoked_at = ? where id = ?", Timestamp.from(clock.now), it.identity.agentId)
        }, "AGENT_REVOKED")

    @Test
    fun `Сверка закрывает поток вытесненного сертификата при наступлении not_after`() =
        supersededWith({ clock.now = notAfter(it) }, "CERT_EXPIRED")
}
