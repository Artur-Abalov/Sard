// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.runs

import dev.sard.server.TestcontainersConfiguration
import dev.sard.server.pki.MovableClock
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.context.annotation.Import
import org.springframework.jdbc.core.JdbcTemplate
import java.sql.Connection
import java.sql.Timestamp
import java.util.UUID
import java.util.concurrent.Callable
import java.util.concurrent.CountDownLatch
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit
import javax.sql.DataSource
import kotlin.test.AfterTest
import kotlin.test.BeforeTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertTrue

private const val RACERS = 8
private const val POLL_MILLIS = 20L

/** Starting a source (S6a tests 1, 7, 8): one active run per source (D6), in the caller's tenant. */
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.NONE,
    properties = ["spring.grpc.server.port=0"],
)
@Import(TestcontainersConfiguration::class, RunsTestConfiguration::class)
class RunsIntegrationTest(
    @Autowired private val sources: Sources,
    @Autowired private val runs: Runs,
    @Autowired private val queued: RecordingStepsQueued,
    @Autowired private val clock: MovableClock,
    @Autowired private val jdbc: JdbcTemplate,
    @Autowired private val dataSource: DataSource,
) {
    private val tenant = RunsTenant(jdbc)
    private val other = RunsTenant(jdbc)

    @BeforeTest
    fun `two tenants with an agent each`() {
        clock.now = RUNS_NOW
        queued.calls.clear()
        tenant.create()
        other.create()
    }

    @AfterTest
    fun `drop both tenants`() {
        tenant.drop()
        other.drop()
    }

    private fun source(owner: RunsTenant = tenant) = sources.create(owner.id, owner.draft())

    @Test
    fun `a started run is one queued backup step with the source's config as of now`() {
        val source = source()

        val run = runs.start(tenant.id, source.id)

        assertEquals(
            RunView(
                run.id,
                source.id,
                source.name,
                false,
                tenant.agentId,
                Trigger.MANUAL,
                RunState.QUEUED,
                null,
                RUNS_NOW,
                null,
                null,
            ),
            run.copy(steps = emptyList()),
        )
        val step = run.steps.single()
        val expected =
            StepView(
                step.id,
                0,
                Action.BACKUP,
                StepState.QUEUED,
                tenant.agentId,
                source.id,
                PLUGIN,
                REPOSITORY,
                source.config,
                RUNS_NOW,
                null,
            )
        assertEquals(expected, step)
        val row =
            jdbc.queryForMap("select run_id, status, config ->> 'database' as db from run_steps where id = ?", step.id)
        assertEquals(mapOf("run_id" to run.id, "status" to "queued", "db" to "app"), row)
    }

    @Test
    fun `a change of the source after the start leaves the step's config as sent`() {
        val source = source()
        val run = runs.start(tenant.id, source.id)
        sources.replace(tenant.id, source.id, tenant.draft(config = """{"database": "other"}"""))

        val sql = "select config ->> 'database' from run_steps where run_id = ?"
        val db = jdbc.queryForObject(sql, String::class.java, run.id)
        assertEquals("app", db)
    }

    @Test
    fun `the dispatcher hears of the step only once the run is committed, in the agent's tenant`() {
        val source = source()
        runs.start(tenant.id, source.id)
        assertEquals(listOf(tenant.id to tenant.agentId), queued.calls.toList())
    }

    @Test
    fun `N concurrent starts of one source make exactly one run, the others learn its id`() {
        val source = source()
        val start = CountDownLatch(1)
        val pool = Executors.newFixedThreadPool(RACERS)
        try {
            val futures =
                (1..RACERS).map {
                    pool.submit(
                        Callable {
                            start.await(RUNS_RACE_WAIT.toSeconds(), TimeUnit.SECONDS)
                            runCatching { runs.start(tenant.id, source.id) }
                        },
                    )
                }
            start.countDown()
            val results = futures.map { it.get(RUNS_RACE_WAIT.toSeconds(), TimeUnit.SECONDS) }

            val winner = results.single { it.isSuccess }.getOrThrow()
            val losers = results.filter { it.isFailure }.map { it.exceptionOrNull() }
            assertEquals(RACERS - 1, losers.size)
            assertTrue(losers.all { it is RunActive && it.activeRunId == winner.id }, "losers: $losers")
            assertEquals(1, tenant.count("runs"))
            assertEquals(1, tenant.count("run_steps"))
            assertEquals(1, queued.calls.size)
        } finally {
            pool.shutdownNow()
        }
    }

    /** Another transaction's active run of [sourceId], inserted but not committed. */
    private fun uncommittedRun(sourceId: UUID): Pair<Connection, UUID> {
        val connection = dataSource.connection.apply { autoCommit = false }
        val id = UUID.randomUUID()
        connection
            .prepareStatement(
                "insert into runs (id, tenant_id, source_id, trigger, status, queued_at) " +
                    "values (?, ?, ?, 'manual', 'queued', ?)",
            ).use {
                it.setObject(1, id)
                it.setObject(2, tenant.id)
                it.setObject(3, sourceId)
                it.setTimestamp(4, Timestamp.from(RUNS_NOW))
                it.executeUpdate()
            }
        return connection to id
    }

    private fun awaitIndexWait() {
        val sql =
            "select count(*) from pg_stat_activity where wait_event_type = 'Lock' and query like 'insert into runs%'"
        val deadline = System.nanoTime() + RUNS_RACE_WAIT.toNanos()
        while (jdbc.queryForObject(sql, Int::class.java) == 0) {
            check(System.nanoTime() < deadline) { "the start never waited on the D6 index" }
            Thread.sleep(POLL_MILLIS)
        }
    }

    @Test
    fun `a start that passes the check but loses on the D6 index gets the winner's id`() {
        val source = source()
        val (other, otherRun) = uncommittedRun(source.id)
        val pool = Executors.newSingleThreadExecutor()
        try {
            val start = pool.submit(Callable { runCatching { runs.start(tenant.id, source.id) } })
            awaitIndexWait()
            other.commit()

            val refused = start.get(RUNS_RACE_WAIT.toSeconds(), TimeUnit.SECONDS).exceptionOrNull()
            assertEquals(otherRun, (refused as RunActive).activeRunId)
            assertEquals(0, tenant.count("run_steps"))
            assertEquals(emptyList(), queued.calls.toList())
        } finally {
            other.close()
            pool.shutdownNow()
        }
    }

    @Test
    fun `a start waiting on the D6 index goes ahead when the other transaction rolls back`() {
        val source = source()
        val (other, _) = uncommittedRun(source.id)
        val pool = Executors.newSingleThreadExecutor()
        try {
            val start = pool.submit(Callable { runs.start(tenant.id, source.id) })
            awaitIndexWait()
            other.rollback()

            assertEquals(RunState.QUEUED, start.get(RUNS_RACE_WAIT.toSeconds(), TimeUnit.SECONDS).status)
            assertEquals(1, tenant.count("runs"))
        } finally {
            other.close()
            pool.shutdownNow()
        }
    }

    @Test
    fun `a start while a run is active is refused with its id, a start after it finished is allowed`() {
        val source = source()
        val first = runs.start(tenant.id, source.id)
        assertEquals(first.id, assertFailsWith<RunActive> { runs.start(tenant.id, source.id) }.activeRunId)

        tenant.finish(first.id)
        val second = runs.start(tenant.id, source.id)

        assertEquals(RunState.QUEUED, second.status)
        assertEquals(2, tenant.count("runs"))
    }

    @Test
    fun `sources of different tenants run independently`() {
        runs.start(tenant.id, source().id)
        runs.start(other.id, source(other).id)
        assertEquals(listOf(1, 1), listOf(tenant.count("runs"), other.count("runs")))
    }

    @Test
    fun `a source of another tenant cannot be started`() {
        val foreign = source(other)
        assertEquals(foreign.id, assertFailsWith<SourceNotFound> { runs.start(tenant.id, foreign.id) }.sourceId)
        assertEquals(0, other.count("runs"))
    }

    @Test
    fun `a deleted source cannot be started`() {
        val source = source()
        sources.delete(tenant.id, source.id)
        assertFailsWith<SourceNotFound> { runs.start(tenant.id, source.id) }
        assertEquals(0, tenant.count("runs"))
        assertEquals(emptyList(), queued.calls.toList())
    }

    @Test
    fun `a repository missing from the agent's last Register refuses the start`() {
        val source = source()
        jdbc.update("delete from agent_repositories where agent_id = ?", tenant.agentId)

        assertEquals(REPOSITORY, assertFailsWith<UnknownRepository> { runs.start(tenant.id, source.id) }.repositoryName)
        assertEquals(0, tenant.count("runs"))
    }

    @Test
    fun `a plugin missing from the agent's last Register refuses the start`() {
        val source = source()
        jdbc.update("delete from agent_plugins where agent_id = ?", tenant.agentId)

        assertEquals(PLUGIN, assertFailsWith<UnknownPlugin> { runs.start(tenant.id, source.id) }.plugin)
        assertEquals(0, tenant.count("runs"))
    }

    @Test
    fun `a source of a revoked agent cannot be started`() {
        val source = source()
        jdbc.update("update agents set revoked_at = now() where id = ?", tenant.agentId)

        assertFailsWith<AgentRevoked> { runs.start(tenant.id, source.id) }
        assertEquals(0, tenant.count("runs"))
    }

    @Test
    fun `an unknown source id is not found`() {
        val missing = UUID.randomUUID()
        assertEquals(missing, assertFailsWith<SourceNotFound> { runs.start(tenant.id, missing) }.sourceId)
    }
}
