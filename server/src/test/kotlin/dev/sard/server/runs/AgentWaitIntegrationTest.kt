// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.runs

import dev.sard.server.TestAdmin
import dev.sard.server.TestcontainersConfiguration
import dev.sard.server.persistence.TenantSessions
import dev.sard.server.persistence.UuidV7
import dev.sard.server.pki.MovableClock
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.context.annotation.Import
import org.springframework.jdbc.core.JdbcTemplate
import java.security.SecureRandom
import java.sql.Timestamp
import java.time.Duration
import java.time.Instant
import kotlin.test.AfterTest
import kotlin.test.BeforeTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNotNull
import kotlin.test.assertNull

private val DAY: Duration = Duration.ofHours(24)
private val GRACE: Duration = Duration.ofMinutes(2)

private const val WAIT_MESSAGE = "the agent was not connected until the wait deadline"

/** The wait deadline of a run whose agent is offline (J1c): set at creation, closed as agent_unavailable at its end. */
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.NONE,
    properties = ["spring.grpc.server.port=0"],
)
@Import(TestcontainersConfiguration::class, RunsTestConfiguration::class, RunFinishedTestConfiguration::class)
class AgentWaitIntegrationTest(
    @Autowired private val sources: Sources,
    @Autowired private val runs: Runs,
    @Autowired private val sessions: TenantSessions,
    @Autowired private val deadlines: StepDeadlines,
    @Autowired private val announcer: RunAnnouncer,
    @Autowired private val finished: RecordingRunFinished,
    @Autowired private val steps: StepTransitions,
    @Autowired private val clock: MovableClock,
    @Autowired private val jdbc: JdbcTemplate,
) {
    private val tenant = RunsTenant(jdbc)

    @BeforeTest
    fun `a tenant with an agent`() {
        clock.now = RUNS_NOW
        finished.heard.clear()
        tenant.create()
    }

    @AfterTest
    fun `drop the tenant`() {
        TestAdmin.of(jdbc).execute("drop trigger if exists fail_wait on runs")
        tenant.drop()
    }

    private fun waits(
        online: Boolean = false,
        startedAt: Instant = RUNS_NOW.minus(Duration.ofDays(1)),
    ) = AgentWaits(deadlines, sessions, clock, announcer, { online }, startedAt, GRACE)

    private fun started(): RunView = runs.start(tenant.id, sources.create(tenant.id, tenant.draft()).id)

    private fun lastSeen(at: Instant?) {
        jdbc.update("update agents set last_seen_at = ? where id = ?", at?.let(Timestamp::from), tenant.agentId)
    }

    /** The run waits until [at], as one created earlier would have. */
    private fun waitsUntil(
        run: RunView,
        at: Instant,
    ) {
        jdbc.update("update runs set agent_wait_until = ? where id = ?", Timestamp.from(at), run.id)
    }

    private fun statusOf(
        table: String,
        key: String,
        run: RunView,
    ) = jdbc.queryForObject("select status from $table where $key = ?", String::class.java, run.id)

    private fun status(run: RunView) = statusOf("runs", "id", run)

    private fun stepStatus(run: RunView) = statusOf("run_steps", "run_id", run)

    private fun runsSeeing(online: Boolean): Runs {
        val ids = UuidV7(clock, SecureRandom())
        return Runs(sessions, clock, ids, RecordingStepsQueued(), DAY, { online })
    }

    // J1c: Запуск онлайн-агента в очереди не называется ждущим
    @Test
    fun `a queued run is waiting for the agent only while the agent is offline`() {
        val run = started()

        assertEquals(RunOutcome.WAITING_FOR_AGENT, runsSeeing(online = false).get(tenant.id, run.id)!!.outcome)
        assertEquals(RunOutcome.QUEUED, runsSeeing(online = true).get(tenant.id, run.id)!!.outcome)
        val listed = runsSeeing(online = false).list(tenant.id, RunFilter(), null, 10).single()
        assertEquals(RunOutcome.WAITING_FOR_AGENT, listed.outcome)
        val online = runsSeeing(online = true).list(tenant.id, RunFilter(), null, 10)
        assertEquals(RunOutcome.QUEUED, online.single().outcome)
    }

    // J1c: the answer to the start of a run says the same as a later read
    @Test
    fun `the run a start creates for an offline agent is waiting for the agent at once`() {
        val source = sources.create(tenant.id, tenant.draft()).id

        val created = runsSeeing(online = false).start(tenant.id, source)

        assertEquals(RunOutcome.WAITING_FOR_AGENT, created.outcome)
        assertEquals(created.outcome, runsSeeing(online = false).get(tenant.id, created.id)!!.outcome)
    }

    // J1e follow-up: the list carries the deadline of the wait and the timeout of the step
    @Test
    fun `a listed run has its wait deadline and the timeout of its step`() {
        val run = started()

        val listed = runsSeeing(online = false).list(tenant.id, RunFilter(), null, 10).single()

        assertEquals(RUNS_NOW + DAY, listed.agentWaitUntil)
        val timeout =
            runs
                .get(tenant.id, run.id)!!
                .steps
                .single()
                .timeoutSeconds
        assertNotNull(timeout)
        assertEquals(timeout, listed.stepTimeoutSeconds)
    }

    // J1c: Ожидание не дольше потолка
    @Test
    fun `a manual run waits for an offline agent for a day and keeps the deadline`() {
        val run = started()

        assertEquals(RUNS_NOW + DAY, run.agentWaitUntil)
        assertEquals(RUNS_NOW + DAY, runs.get(tenant.id, run.id)!!.agentWaitUntil)
    }

    // J1c: Потолок ожидания задаётся настройкой
    @Test
    fun `the ceiling of the wait is the setting`() {
        val short = Runs(sessions, clock, UuidV7(clock, SecureRandom()), RecordingStepsQueued(), Duration.ofMinutes(1))
        val run = short.start(tenant.id, sources.create(tenant.id, tenant.draft()).id)

        assertEquals(RUNS_NOW + Duration.ofMinutes(1), run.agentWaitUntil)
    }

    // J1c: Срок ожидания истёк - запуск не выполнен, агент был недоступен
    @Test
    fun `a run whose wait is over and whose agent is offline is not run`() {
        val run = started()
        val lastContact = RUNS_NOW + DAY - Duration.ofHours(3)
        lastSeen(lastContact)
        clock.now = RUNS_NOW + DAY

        val closed = waits().expire()

        assertEquals(1, closed)
        assertEquals("agent_unavailable", status(run))
        assertEquals("agent_unavailable", stepStatus(run))
        val stored = runs.get(tenant.id, run.id)!!
        assertEquals(RunOutcome.AGENT_UNAVAILABLE, stored.outcome)
        assertEquals(WAIT_MESSAGE, stored.message)
        assertEquals(WAIT_MESSAGE, stored.steps.single().message)
        assertEquals(lastContact, stored.agentUnavailableSince)
        assertEquals(clock.now, stored.finishedAt)
        assertNull(stored.agentWaitUntil)
        assertNull(stored.steps.single().dispatchedAt)
    }

    // J1c: Агент, ни разу не подключавшийся, даёт agentUnavailableSince null
    @Test
    fun `an agent that never connected leaves no last contact`() {
        val run = started()
        clock.now = RUNS_NOW + DAY

        waits().expire()

        assertEquals("agent_unavailable", status(run))
        assertNull(runs.get(tenant.id, run.id)!!.agentUnavailableSince)
    }

    // J1c: До срока ожидания запуск ждёт
    @Test
    fun `a run waits until its deadline and shows it waits`() {
        val run = started()
        clock.now = RUNS_NOW + DAY - Duration.ofSeconds(1)

        assertEquals(0, waits().expire())

        assertEquals("queued", status(run))
    }

    // J1c: Онлайн-агент в момент срока - запуск не закрывается
    @Test
    fun `a run of an online agent is not closed at its deadline`() {
        val run = started()
        clock.now = RUNS_NOW + DAY

        assertEquals(0, waits(online = true).expire())

        assertEquals("queued", status(run))
    }

    // J1c: Истечение ожидания освобождает источник
    @Test
    fun `the source is free once the wait expired`() {
        val run = started()
        clock.now = RUNS_NOW + DAY
        waits().expire()

        val next = runs.start(tenant.id, run.sourceId)

        assertEquals(RunState.QUEUED, next.status)
    }

    // J1c: Отказ базы на тике ожидания - повтор на следующем тике без двойного закрытия
    @Test
    fun `a second tick closes nothing again and announces the run once`() {
        val run = started()
        clock.now = RUNS_NOW + DAY
        val waits = waits()

        assertEquals(1, waits.expire())
        assertEquals(0, waits.expire())

        assertEquals(listOf(run.id), finished.heard.map { it.event.runId })
        assertEquals(
            RunState.AGENT_UNAVAILABLE,
            finished.heard
                .single()
                .event.status,
        )
    }

    // J1c: Агент подключается - он не получает шаг, срок которого истёк
    @Test
    fun `the agent that connects later is not given the step of an expired run`() {
        started()
        clock.now = RUNS_NOW + DAY
        waits().expire()

        assertEquals(emptyList(), steps.active(tenant.id, tenant.agentId))
    }

    // J1c: Отказ базы на тике ожидания - повтор на следующем тике без двойного закрытия
    @Test
    fun `a database failure on the tick leaves the run waiting, the next tick closes it once`() {
        val run = started()
        clock.now = RUNS_NOW + DAY
        TestAdmin.of(jdbc).execute(
            """
            create or replace function fail_wait() returns trigger language plpgsql as
            ${'$'}${'$'} begin raise exception 'database unavailable'; end ${'$'}${'$'}
            """.trimIndent(),
        )
        TestAdmin.of(jdbc).execute(
            "create trigger fail_wait before update on runs for each row " +
                "when (new.status = 'agent_unavailable') execute function fail_wait()",
        )
        val waits = waits()

        assertEquals(0, waits.expire())
        assertEquals("queued", status(run))
        assertEquals("queued", stepStatus(run))
        assertEquals(emptyList(), finished.heard.toList())

        TestAdmin.of(jdbc).execute("drop trigger fail_wait on runs")
        assertEquals(1, waits.expire())
        assertEquals(0, waits.expire())
        assertEquals(listOf(run.id), finished.heard.map { it.event.runId })
    }

    // J1c: После простоя сервера дольше срока ожидания запуск ждёт отсрочку после старта
    @Test
    fun `after a start the wait of an agent that has not reconnected gets the grace`() {
        val run = started()
        waitsUntil(run, RUNS_NOW + Duration.ofHours(1))
        lastSeen(RUNS_NOW + Duration.ofMinutes(5))
        val start = RUNS_NOW + Duration.ofHours(3)
        clock.now = start + Duration.ofSeconds(90)

        assertEquals(0, waits(startedAt = start).expire())
        assertEquals("queued", status(run))
    }

    // J1c: После простоя сервера агент не подключился за отсрочку - агент был недоступен
    @Test
    fun `an agent that did not connect within the grace after a start was unavailable`() {
        val run = started()
        waitsUntil(run, RUNS_NOW + Duration.ofHours(1))
        val start = RUNS_NOW + Duration.ofHours(3)
        clock.now = start + GRACE

        assertEquals(1, waits(startedAt = start).expire())
        assertEquals("agent_unavailable", status(run))
    }

    // J1c: ДJ9 - переподключившийся и снова пропавший агент получает обычное окно, без отсрочки
    @Test
    fun `an agent that reconnected after the start and dropped again has no grace`() {
        val run = started()
        waitsUntil(run, RUNS_NOW + Duration.ofHours(1))
        val start = RUNS_NOW + Duration.ofHours(3)
        lastSeen(start + Duration.ofSeconds(10))
        clock.now = start + Duration.ofSeconds(30)

        assertEquals(1, waits(startedAt = start).expire())
        assertEquals("agent_unavailable", status(run))
    }

    @Test
    fun `a run of another tenant is closed in its own tenant`() {
        val other = RunsTenant(jdbc).create()
        try {
            val mine = started()
            val theirs = runs.start(other.id, sources.create(other.id, other.draft()).id)
            clock.now = RUNS_NOW + DAY

            assertEquals(2, waits().expire())

            assertEquals("agent_unavailable", status(mine))
            assertEquals("agent_unavailable", status(theirs))
        } finally {
            other.drop()
        }
    }

    @Test
    fun `a run without a deadline never expires`() {
        val run = started()
        jdbc.update("update runs set agent_wait_until = null where id = ?", run.id)
        clock.now = RUNS_NOW + DAY.multipliedBy(2)

        assertEquals(0, waits().expire())
        assertEquals("queued", status(run))
    }
}
