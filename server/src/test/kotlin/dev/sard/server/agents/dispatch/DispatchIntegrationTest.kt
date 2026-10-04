// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents.dispatch

import dev.sard.proto.agent.v1.Action
import dev.sard.proto.agent.v1.ConnectResponse
import dev.sard.proto.agent.v1.RunStep
import dev.sard.server.TestcontainersConfiguration
import dev.sard.server.agents.stream.AgentSessionRegistry
import dev.sard.server.agents.stream.CommandReconciliation
import dev.sard.server.agents.stream.ConnectedAgent
import dev.sard.server.agents.stream.Connection
import dev.sard.server.agents.stream.StreamClients
import dev.sard.server.agents.stream.TestAgent
import dev.sard.server.agents.stream.WAIT_SECONDS
import dev.sard.server.enrollment.Enrollment
import dev.sard.server.enrollment.EnrollmentTokens
import dev.sard.server.pki.CertificateAuthority
import dev.sard.server.pki.MovableClock
import dev.sard.server.runs.ProgressReport
import dev.sard.server.runs.RunView
import dev.sard.server.runs.Runs
import dev.sard.server.runs.SourceDraft
import dev.sard.server.runs.Sources
import dev.sard.server.runs.StepDeadlines
import dev.sard.server.runs.StepOutcome
import dev.sard.server.runs.StepProgressWrites
import dev.sard.server.runs.StepState
import dev.sard.server.runs.StepTransitions
import io.micrometer.core.instrument.MeterRegistry
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.grpc.test.autoconfigure.LocalGrpcServerPort
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.boot.test.context.TestConfiguration
import org.springframework.context.annotation.Bean
import org.springframework.context.annotation.Import
import org.springframework.context.annotation.Primary
import org.springframework.jdbc.core.JdbcTemplate
import java.sql.Timestamp
import java.time.Duration
import java.time.Instant
import java.time.temporal.ChronoUnit
import java.util.UUID
import java.util.concurrent.LinkedBlockingQueue
import java.util.concurrent.TimeUnit
import kotlin.test.AfterTest
import kotlin.test.BeforeTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNotNull
import kotlin.test.assertNull
import kotlin.test.assertTrue

private const val POLL_MILLIS = 20L
private val QUIET: Duration = Duration.ofMillis(300)

/** The real dispatcher behind the extension point, telling the test when a Hello has been reconciled. */
class ReconciledHellos(
    private val dispatcher: StepDispatcher,
) : CommandReconciliation {
    private val done = LinkedBlockingQueue<UUID>()

    override fun onHello(
        agent: ConnectedAgent,
        runningCommandIds: List<String>,
    ) {
        dispatcher.onHello(agent, runningCommandIds)
        done += agent.agentId
    }

    fun await(agentId: UUID) {
        val next = done.poll(WAIT_SECONDS, TimeUnit.SECONDS)
        assertEquals(agentId, next, "no reconciled Hello of $agentId")
    }
}

@TestConfiguration(proxyBeanMethods = false)
class DispatchTestConfiguration {
    /** A whole second, so Postgres keeps it exactly; certificates are issued at it. */
    @Bean
    fun clock() = MovableClock(Instant.now().truncatedTo(ChronoUnit.SECONDS))

    @Bean
    @Primary
    fun reconciledHellos(dispatcher: StepDispatcher) = ReconciledHellos(dispatcher)
}

/** S6a tests 2-5 and 7, FXs tests 1-4, over the real server: TLS, PostgreSQL, Connect streams opened by the test. */
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.RANDOM_PORT,
    // The periodic checks stay out of the way; the tests tick by hand after moving the clock.
    properties = ["spring.grpc.server.port=0", "sard.agent.stream.check-interval=1h"],
)
@Import(TestcontainersConfiguration::class, DispatchTestConfiguration::class)
class DispatchIntegrationTest(
    @Autowired ca: CertificateAuthority,
    @Autowired enrollment: Enrollment,
    @Autowired tokens: EnrollmentTokens,
    @LocalGrpcServerPort port: Int,
    @Autowired private val clock: MovableClock,
    @Autowired private val hellos: ReconciledHellos,
    @Autowired private val dispatcher: StepDispatcher,
    @Autowired private val settings: DispatchSettings,
    @Autowired private val registry: AgentSessionRegistry,
    @Autowired private val sources: Sources,
    @Autowired private val runs: Runs,
    @Autowired private val steps: StepTransitions,
    @Autowired private val deadlines: StepDeadlines,
    @Autowired private val progress: StepProgressWrites,
    @Autowired private val meters: MeterRegistry,
    @Autowired private val jdbc: JdbcTemplate,
) {
    private val clients = StreamClients(ca, enrollment, tokens, jdbc, port)
    private val other = StreamClients(ca, enrollment, tokens, jdbc, port)
    private val start = clock.now

    @BeforeTest
    fun `fresh tenants`() {
        clock.now = start
        clients.createTenant()
        other.createTenant()
    }

    @AfterTest
    fun `drop the tenants`() {
        for (tenant in listOf(clients, other)) {
            tenant.closeChannels()
            for (table in listOf("run_steps", "runs", "sources", "agent_plugins", "agent_repositories")) {
                jdbc.update("delete from $table where tenant_id = ?", tenant.tenant)
            }
            tenant.close()
        }
    }

    /** An enrolled agent whose last Register offered postgresql and the repository main. */
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

    private fun run(
        agent: TestAgent,
        name: String = "prod-db",
    ): RunView {
        val draft = SourceDraft(name, agent.agentId, "postgresql", "main", """{"database": "$name"}""")
        val source = sources.create(agent.tenantId, draft)
        return runs.start(agent.tenantId, source.id)
    }

    private fun greeted(
        tenant: StreamClients,
        agent: TestAgent,
        vararg running: UUID,
    ): Connection =
        tenant.connect(agent).also {
            it.hello(*running.map(UUID::toString).toTypedArray())
            hellos.await(agent.agentId)
        }

    private fun Connection.nextStep(): RunStep {
        val message = checkNotNull(received.poll(WAIT_SECONDS, TimeUnit.SECONDS)) { "no RunStep" }
        assertEquals(ConnectResponse.MessageCase.RUN_STEP, message.messageCase)
        return message.runStep
    }

    private fun Connection.nothingMore() = assertNull(received.poll(QUIET.toMillis(), TimeUnit.MILLISECONDS))

    private fun Connection.leave(agent: TestAgent) {
        clients.closeChannels()
        val deadline = System.nanoTime() + Duration.ofSeconds(WAIT_SECONDS).toNanos()
        while (registry.session(agent.agentId) != null) {
            check(System.nanoTime() < deadline) { "the server kept the session of ${agent.agentId}" }
            Thread.sleep(POLL_MILLIS)
        }
    }

    private fun status(stepId: UUID): Map<String, Any?> =
        jdbc.queryForMap("select status, dispatched_at from run_steps where id = ?", stepId)

    private fun deadline(stepId: UUID): Instant? =
        jdbc
            .queryForObject("select lost_deadline from run_steps where id = ?", Timestamp::class.java, stepId)
            ?.toInstant()

    /** The session's end reaches the listener just after the registry lets the slot go. */
    private fun awaitDeadline(stepId: UUID): Instant {
        val until = System.nanoTime() + Duration.ofSeconds(WAIT_SECONDS).toNanos()
        while (deadline(stepId) == null) {
            check(System.nanoTime() < until) { "step $stepId got no lost deadline" }
            Thread.sleep(POLL_MILLIS)
        }
        return checkNotNull(deadline(stepId))
    }

    /** A running step of [agent] whose session then ended. */
    private fun runningThenLeft(agent: TestAgent): RunView {
        val connection = greeted(clients, agent)
        val run = run(agent)
        connection.nextStep()
        steps.accepted(clients.tenant, run.steps.single().id, "accepted")
        connection.leave(agent)
        return run
    }

    private fun runStatus(runId: UUID): String? {
        val sql = "select status from runs where id = ?"
        return jdbc.queryForObject(sql, String::class.java, runId)
    }

    // --- 2: online agent

    @Test
    fun `a run of an online agent's source reaches it as a RunStep and is dispatched`() {
        val agent = agent()
        val connection = greeted(clients, agent)

        val run = run(agent)

        val step = connection.nextStep()
        val stepId = run.steps.single().id
        assertEquals(
            listOf(stepId.toString(), "postgresql", "main", Action.ACTION_BACKUP),
            listOf(step.commandId, step.plugin, step.repositoryName, step.action),
        )
        assertEquals("""{"database": "prod-db"}""", step.configJson)
        assertEquals(mapOf("status" to "dispatched", "dispatched_at" to Timestamp.from(clock.now)), status(stepId))
        assertEquals("dispatched", runStatus(run.id))
    }

    // --- 3: offline agent, delivered on connect in creation order

    @Test
    fun `an offline agent's steps wait in the queue and arrive on connect in creation order`() {
        val agent = agent()
        val created =
            listOf("a", "b", "c").map { name ->
                clock.now += Duration.ofSeconds(1)
                run(agent, name).steps.single().id
            }
        assertEquals(listOf("queued", "queued", "queued"), created.map { status(it)["status"] })

        val connection = greeted(clients, agent)

        assertEquals(created.map { it.toString() }, List(3) { connection.nextStep().commandId })
        assertEquals(listOf("dispatched", "dispatched", "dispatched"), created.map { status(it)["status"] })
        connection.nothingMore()
    }

    // --- 4: a step back in the queue goes out at the next check

    @Test
    fun `a step the session refused is delivered by the next check while the agent is online`() {
        val agent = agent()
        val connection = greeted(clients, agent)
        val stepId = run(agent).steps.single().id
        connection.nextStep()
        // What StepDispatcher does when send answers QueueFull or NotConnected (StepDispatcherTest).
        steps.release(clients.tenant, stepId)

        dispatcher.tick()

        assertEquals(stepId.toString(), connection.nextStep().commandId)
        assertEquals("dispatched", status(stepId)["status"])
    }

    // --- 5: reconciliation on Hello

    @Test
    fun `a step the Hello lists is not sent again`() {
        val agent = agent()
        val first = greeted(clients, agent)
        val stepId = run(agent).steps.single().id
        first.nextStep()
        first.leave(agent)
        clock.now += Duration.ofSeconds(1)

        val second = greeted(clients, agent, stepId)
        clock.now += settings.lostAfter
        dispatcher.tick()

        second.nothingMore()
        assertEquals("dispatched", status(stepId)["status"])
    }

    @Test
    fun `a dispatched step the Hello does not list is sent again and counted`() {
        val agent = agent()
        val first = greeted(clients, agent)
        val stepId = run(agent).steps.single().id
        first.nextStep()
        first.leave(agent)
        val before = meters.counter("sard.run.steps.redispatched").count()
        clock.now += Duration.ofSeconds(1)

        val second = greeted(clients, agent)

        assertEquals(stepId.toString(), second.nextStep().commandId)
        assertEquals(Timestamp.from(clock.now), status(stepId)["dispatched_at"])
        assertEquals(before + 1, meters.counter("sard.run.steps.redispatched").count())
        second.nothingMore()
    }

    @Test
    fun `a running step the Hello does not list is lost after the window, and never sent again`() {
        val agent = agent()
        val first = greeted(clients, agent)
        val run = run(agent)
        val stepId = run.steps.single().id
        first.nextStep()
        steps.accepted(clients.tenant, stepId, "accepted")
        first.leave(agent)

        val second = greeted(clients, agent)
        clock.now += settings.lostAfter - Duration.ofSeconds(1)
        dispatcher.tick()
        assertEquals("running", status(stepId)["status"])

        clock.now += Duration.ofSeconds(1)
        dispatcher.tick()

        assertEquals("lost", status(stepId)["status"])
        assertEquals("failed", runStatus(run.id))
        second.nothingMore()
    }

    @Test
    fun `a running step whose result arrives within the window is not lost`() {
        val agent = agent()
        val first = greeted(clients, agent)
        val stepId = run(agent).steps.single().id
        first.nextStep()
        steps.accepted(clients.tenant, stepId, "accepted")
        first.leave(agent)

        greeted(clients, agent)
        // The agent resends its stored result right after Hello (A3); S7 records it through this call.
        steps.finished(clients.tenant, stepId, StepOutcome(StepState.SUCCEEDED, null))
        clock.now += settings.lostAfter
        dispatcher.tick()

        assertEquals("succeeded", status(stepId)["status"])
    }

    // --- FXs 1: an agent that does not come back

    @Test
    fun `a running step of an agent that never comes back is lost one window after its session ended`() {
        val agent = agent()
        val run = runningThenLeft(agent)
        val stepId = run.steps.single().id
        assertEquals(clock.now + settings.lostAfter, awaitDeadline(stepId))

        clock.now += settings.lostAfter - Duration.ofSeconds(1)
        dispatcher.tick()
        assertEquals("running", status(stepId)["status"])

        clock.now += Duration.ofSeconds(1)
        dispatcher.tick()

        assertEquals(listOf("lost", "failed"), listOf(status(stepId)["status"], runStatus(run.id)))
        val next = runs.start(agent.tenantId, run.sourceId)
        assertEquals("queued", runStatus(next.id), "the source runs again")
    }

    @Test
    fun `a dispatched step of an agent that never comes back is lost too`() {
        val agent = agent()
        val connection = greeted(clients, agent)
        val stepId = run(agent).steps.single().id
        connection.nextStep()
        connection.leave(agent)
        awaitDeadline(stepId)

        clock.now += settings.lostAfter
        dispatcher.tick()

        assertEquals("lost", status(stepId)["status"])
    }

    // --- FXs 2: a server restart, the agent away

    @Test
    fun `after a restart a step in flight gets a fresh window from the start, then is lost`() {
        val agent = agent()
        val stepId = runningThenLeft(agent).steps.single().id
        awaitDeadline(stepId)
        clock.now += settings.lostAfter.multipliedBy(10)
        // The new process: a dispatcher over the same database, no agent connected yet.
        val restarted =
            StepDispatcher(steps, deadlines, FakeLinks(), clock, settings.lostAfter, RecordingDispatchMetrics()) {
                dev.sard.server.runs
                    .WaitingSteps(0, 0)
            }

        restarted.onStart()
        restarted.tick()
        assertEquals("running", status(stepId)["status"], "the server's downtime is not the agent's")
        assertEquals(clock.now + settings.lostAfter, deadline(stepId))

        clock.now += settings.lostAfter
        restarted.tick()
        assertEquals("lost", status(stepId)["status"])
    }

    @Test
    fun `a server that stops sets no deadline, its start does`() {
        val agent = agent()
        val connection = greeted(clients, agent)
        val stepId = run(agent).steps.single().id
        connection.nextStep()

        dispatcher.onDisconnected(ConnectedAgent(agent.agentId, agent.tenantId, ""), "SERVER_SHUTTING_DOWN")

        assertNull(deadline(stepId))
    }

    // --- FXs 3: reconnects more often than the window

    @Test
    fun `reconnects without the step more often than the window do not postpone its loss`() {
        val agent = agent()
        val stepId = runningThenLeft(agent).steps.single().id
        val first = awaitDeadline(stepId)
        repeat(3) {
            clock.now += settings.lostAfter.dividedBy(4)
            greeted(clients, agent).leave(agent)
        }
        assertEquals(first, deadline(stepId))

        clock.now = first
        dispatcher.tick()

        assertEquals("lost", status(stepId)["status"])
    }

    // --- FXs 4: what clears the deadline, and the race with a result

    @Test
    fun `a Hello that lists the step clears its deadline, and it is not lost`() {
        val agent = agent()
        val stepId = runningThenLeft(agent).steps.single().id
        awaitDeadline(stepId)

        greeted(clients, agent, stepId)
        clock.now += settings.lostAfter
        dispatcher.tick()

        assertNull(deadline(stepId))
        assertEquals("running", status(stepId)["status"])
    }

    @Test
    fun `progress before the deadline clears it`() {
        val agent = agent()
        val stepId = runningThenLeft(agent).steps.single().id
        awaitDeadline(stepId)

        progress.write(agent.tenantId, agent.agentId, stepId, ProgressReport("uploading", 10, 100))
        clock.now += settings.lostAfter
        dispatcher.tick()

        assertNull(deadline(stepId))
        assertEquals("running", status(stepId)["status"])
    }

    @Test
    fun `a dispatched step sent again after a Hello has no deadline`() {
        val agent = agent()
        val first = greeted(clients, agent)
        val stepId = run(agent).steps.single().id
        first.nextStep()
        first.leave(agent)
        awaitDeadline(stepId)
        clock.now += Duration.ofSeconds(1)

        greeted(clients, agent).nextStep()

        assertNull(deadline(stepId))
    }

    @Test
    fun `a result after the deadline but before the check wins, the check then moves nothing`() {
        val agent = agent()
        val run = runningThenLeft(agent)
        val stepId = run.steps.single().id
        awaitDeadline(stepId)
        clock.now += settings.lostAfter

        assertTrue(steps.finished(clients.tenant, stepId, StepOutcome(StepState.SUCCEEDED, null)))
        assertFalse(steps.lost(clients.tenant, stepId))

        assertEquals(listOf("succeeded", "succeeded"), listOf(status(stepId)["status"], runStatus(run.id)))
    }

    @Test
    fun `a check before the result wins, the late result moves nothing`() {
        val agent = agent()
        val run = runningThenLeft(agent)
        val stepId = run.steps.single().id
        awaitDeadline(stepId)
        clock.now += settings.lostAfter

        assertTrue(steps.lost(clients.tenant, stepId))
        assertFalse(steps.finished(clients.tenant, stepId, StepOutcome(StepState.SUCCEEDED, null)))

        assertEquals(listOf("lost", "failed"), listOf(status(stepId)["status"], runStatus(run.id)))
    }

    @Test
    fun `a step before its deadline is not lost, whoever asks`() {
        val agent = agent()
        val stepId = runningThenLeft(agent).steps.single().id
        assertNotNull(awaitDeadline(stepId))

        assertFalse(steps.lost(clients.tenant, stepId))
        assertEquals(emptyList(), deadlines.overdue(clock.now).filter { it.id == stepId })
    }

    // --- 7: tenants

    @Test
    fun `an agent of another tenant never sees this tenant's steps`() {
        val mine = agent()
        val stepId = run(mine).steps.single().id
        val foreign = agent(other)

        val connection = greeted(other, foreign)
        dispatcher.tick()

        connection.nothingMore()
        assertEquals("queued", status(stepId)["status"])
    }

    // --- metric

    @Test
    fun `the check counts steps waiting in the queue and dispatched`() {
        val offline = agent()
        run(offline, "a")
        val online = agent()
        val connection = greeted(clients, online)
        run(online, "b")
        connection.nextStep()

        dispatcher.tick()

        assertEquals(1.0, meters.get("sard.run.steps.queued").gauge().value())
        assertEquals(1.0, meters.get("sard.run.steps.dispatched").gauge().value())
    }
}
