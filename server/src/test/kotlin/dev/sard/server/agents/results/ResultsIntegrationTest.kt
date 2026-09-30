// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents.results

import dev.sard.proto.agent.v1.BackupOutput
import dev.sard.proto.agent.v1.ConnectResponse
import dev.sard.proto.agent.v1.StepResult
import dev.sard.proto.agent.v1.StepStatus
import dev.sard.server.TestcontainersConfiguration
import dev.sard.server.agents.dispatch.DispatchTestConfiguration
import dev.sard.server.agents.dispatch.ReconciledHellos
import dev.sard.server.agents.stream.AgentSessionRegistry
import dev.sard.server.agents.stream.Connection
import dev.sard.server.agents.stream.StreamClients
import dev.sard.server.agents.stream.TestAgent
import dev.sard.server.agents.stream.WAIT_SECONDS
import dev.sard.server.enrollment.Enrollment
import dev.sard.server.enrollment.EnrollmentTokens
import dev.sard.server.pki.CertificateAuthority
import dev.sard.server.runs.RunView
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
import java.time.Duration
import java.util.UUID
import java.util.concurrent.TimeUnit
import kotlin.test.AfterTest
import kotlin.test.BeforeTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNotEquals
import kotlin.test.assertNull

private const val REPOSITORY_ID = "5f0c3e2d9a8b7c6d5e4f3a2b1c0d9e8f7a6b5c4d3e2f1a0b9c8d7e6f5a4b3c2d"
private const val POLL_MILLIS = 20L
private val QUIET: Duration = Duration.ofMillis(300)

/**
 * S7a tests 1–3 over the real server: TLS, PostgreSQL and Connect streams opened by the test in
 * the agent's place. ResultAck is the agent's only signal to drop a result, so each test checks
 * what the database holds at the moment the ack arrives.
 */
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.RANDOM_PORT,
    properties = ["spring.grpc.server.port=0", "sard.agent.stream.check-interval=1h"],
)
@Import(TestcontainersConfiguration::class, DispatchTestConfiguration::class)
class ResultsIntegrationTest(
    @Autowired ca: CertificateAuthority,
    @Autowired enrollment: Enrollment,
    @Autowired tokens: EnrollmentTokens,
    @LocalGrpcServerPort port: Int,
    @Autowired private val hellos: ReconciledHellos,
    @Autowired private val registry: AgentSessionRegistry,
    @Autowired private val sources: Sources,
    @Autowired private val runs: Runs,
    @Autowired private val meters: MeterRegistry,
    @Autowired private val jdbc: JdbcTemplate,
) {
    private val clients = StreamClients(ca, enrollment, tokens, jdbc, port)
    private val other = StreamClients(ca, enrollment, tokens, jdbc, port)

    @BeforeTest
    fun `fresh tenants`() {
        clients.createTenant()
        other.createTenant()
    }

    @AfterTest
    fun `drop the tenants`() {
        jdbc.update("drop trigger if exists snapshots_fail on snapshots")
        for (tenant in listOf(clients, other)) {
            tenant.closeChannels()
            val tables = listOf("snapshots", "run_steps", "runs", "sources", "agent_plugins", "agent_repositories")
            for (table in tables) jdbc.update("delete from $table where tenant_id = ?", tenant.tenant)
            tenant.close()
        }
    }

    private fun agent(tenant: StreamClients = clients): TestAgent {
        val agent = tenant.enrolled()
        jdbc.update(
            """
            insert into agent_plugins (tenant_id, agent_id, name, version, config_schema, actions)
            values (?, ?, 'postgresql', '0.1.0', '{}'::jsonb, array['backup'])
            """.trimIndent(),
            agent.tenantId,
            agent.agentId,
        )
        jdbc.update(
            "insert into agent_repositories (tenant_id, agent_id, name, backend) values (?, ?, 'main', 'local')",
            agent.tenantId,
            agent.agentId,
        )
        return agent
    }

    private fun greeted(
        tenant: StreamClients,
        agent: TestAgent,
    ): Connection =
        tenant.connect(agent).also {
            it.hello()
            hellos.await(agent.agentId)
        }

    /** A run of [agent]'s source whose RunStep reached [connection]. */
    private fun dispatched(
        agent: TestAgent,
        connection: Connection,
    ): RunView {
        val draft = SourceDraft("prod-db", agent.agentId, "postgresql", "main", """{"database": "app"}""")
        val run = runs.start(agent.tenantId, sources.create(agent.tenantId, draft).id)
        val message = checkNotNull(connection.received.poll(WAIT_SECONDS, TimeUnit.SECONDS)) { "no RunStep" }
        assertEquals(ConnectResponse.MessageCase.RUN_STEP, message.messageCase)
        return run
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

    /** The next ResultAck on [this], skipping RunSteps sent again after a Hello. */
    private fun Connection.nextAck(): String {
        while (true) {
            val message = checkNotNull(received.poll(WAIT_SECONDS, TimeUnit.SECONDS)) { "no ResultAck" }
            if (message.messageCase == ConnectResponse.MessageCase.RESULT_ACK) return message.resultAck.commandId
        }
    }

    private fun Connection.noAck() {
        val message = received.poll(QUIET.toMillis(), TimeUnit.MILLISECONDS)
        assertNull(message?.takeIf { it.messageCase == ConnectResponse.MessageCase.RESULT_ACK })
    }

    private fun leave(agent: TestAgent) {
        clients.closeChannels()
        val deadline = System.nanoTime() + Duration.ofSeconds(WAIT_SECONDS).toNanos()
        while (registry.session(agent.agentId) != null) {
            check(System.nanoTime() < deadline) { "the server kept the session of ${agent.agentId}" }
            Thread.sleep(POLL_MILLIS)
        }
    }

    private fun statuses(run: RunView): List<Any?> {
        val step = jdbc.queryForObject("select status from run_steps where run_id = ?", String::class.java, run.id)
        val runStatus = jdbc.queryForObject("select status from runs where id = ?", String::class.java, run.id)
        return listOf(step, runStatus)
    }

    private fun snapshots(run: RunView): Int? =
        jdbc.queryForObject(
            "select count(*) from snapshots where step_id = (select id from run_steps where run_id = ?)",
            Int::class.java,
            run.id,
        )

    private fun counted(outcome: String) =
        meters
            .find("sard.run.step.results")
            .tag("outcome", outcome)
            .counter()
            ?.count() ?: 0.0

    // --- 1: the result is recorded, then acknowledged

    @Test
    fun `a result is acknowledged once its step, run and snapshot are committed`() {
        val agent = agent()
        val connection = greeted(clients, agent)
        val run = dispatched(agent, connection)
        val step = run.steps.single().id
        val before = counted("succeeded")

        connection.result(succeeded(step))

        assertEquals(step.toString(), connection.nextAck())
        assertEquals(listOf<Any?>("succeeded", "succeeded"), statuses(run))
        assertEquals(1, snapshots(run))
        assertEquals(before + 1, counted("succeeded"))
    }

    @Test
    fun `a database failure ends the stream without ResultAck, and the agent's resend is recorded`() {
        val agent = agent()
        val connection = greeted(clients, agent)
        val run = dispatched(agent, connection)
        val step = run.steps.single().id
        jdbc.execute(
            """
            create or replace function snapshots_fail() returns trigger language plpgsql as
            ${'$'}${'$'} begin raise exception 'disk full'; end ${'$'}${'$'};
            create trigger snapshots_fail before insert on snapshots for each row execute function snapshots_fail();
            """.trimIndent(),
        )

        connection.result(succeeded(step))

        assertNotEquals(Status.Code.OK, connection.ended().code)
        assertNull(connection.received.firstOrNull { it.messageCase == ConnectResponse.MessageCase.RESULT_ACK })
        assertEquals(listOf<Any?>("dispatched", "dispatched"), statuses(run))

        jdbc.update("drop trigger snapshots_fail on snapshots")
        leave(agent)
        val again = greeted(clients, agent)
        again.result(succeeded(step))

        assertEquals(step.toString(), again.nextAck())
        assertEquals(listOf<Any?>("succeeded", "succeeded"), statuses(run))
        assertEquals(1, snapshots(run))
    }

    // --- 2: a repeated result

    @Test
    fun `a result sent again on a new stream is acknowledged again and changes nothing`() {
        val agent = agent()
        val connection = greeted(clients, agent)
        val run = dispatched(agent, connection)
        val step = run.steps.single().id
        connection.result(succeeded(step))
        connection.nextAck()
        val row = jdbc.queryForMap("select * from run_steps where id = ?", step)
        val before = counted("repeated")

        leave(agent)
        val again = greeted(clients, agent)
        again.result(succeeded(step))

        assertEquals(step.toString(), again.nextAck())
        assertEquals(row, jdbc.queryForMap("select * from run_steps where id = ?", step))
        assertEquals(1, snapshots(run))
        assertEquals(before + 1, counted("repeated"))
    }

    // --- 3: someone else's command

    @Test
    fun `a result for another agent's step is acknowledged and not recorded`() {
        val owner = agent()
        val ownerConnection = greeted(clients, owner)
        val run = dispatched(owner, ownerConnection)
        val step = run.steps.single().id
        val sibling = agent()
        val stranger = agent(other)
        val before = counted("unknown")

        for ((tenant, intruder) in listOf(clients to sibling, other to stranger)) {
            val connection = greeted(tenant, intruder)
            connection.result(succeeded(step))

            assertEquals(step.toString(), connection.nextAck())
        }

        assertEquals(listOf<Any?>("dispatched", "dispatched"), statuses(run))
        assertEquals(0, snapshots(run))
        assertEquals(before + 2, counted("unknown"))
        ownerConnection.noAck()
    }
}
