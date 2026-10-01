// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import dev.sard.proto.agent.v1.StepStatus
import dev.sard.server.agents.dispatch.ReconciledHellos
import dev.sard.server.enrollment.Enrollment
import dev.sard.server.enrollment.EnrollmentTokens
import dev.sard.server.pki.CertificateAuthority
import dev.sard.server.pki.MovableClock
import dev.sard.server.registration.Registration
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.beans.factory.annotation.Value
import org.springframework.boot.test.web.server.LocalServerPort
import org.springframework.jdbc.core.JdbcTemplate
import tools.jackson.databind.ObjectMapper
import java.util.UUID
import java.util.concurrent.Callable
import java.util.concurrent.CountDownLatch
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit
import kotlin.test.AfterTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

private const val RACE_SECONDS = 20L

/** Rules "Отзыв агента сразу закрывает его стрим и сохраняет историю" and "Изменение источника" with real agents. */
@RestApiTest
class AgentStepsApiIntegrationTest(
    @Autowired ca: CertificateAuthority,
    @Autowired enrollment: Enrollment,
    @Autowired tokens: EnrollmentTokens,
    @Autowired registration: Registration,
    @Autowired jdbc: JdbcTemplate,
    @Autowired clock: MovableClock,
    @Autowired private val mapper: ObjectMapper,
    @Autowired private val hellos: ReconciledHellos,
    @LocalServerPort port: Int,
    @Value("\${local.grpc.server.port}") grpc: Int,
) {
    private val world = RestWorld(ca, enrollment, tokens, registration, jdbc, clock, mapper, port, grpc)
    private val tenant = world.tenant()
    private val admin = world.admin(tenant)
    private val agent = world.agent(tenant)

    @AfterTest
    fun `drop the tenants`() = world.close()

    private fun connected(who: dev.sard.server.agents.stream.TestAgent = agent): FakeAgent =
        FakeAgent(world.connect(who).also { it.hello() }).also { hellos.await(who.agentId) }

    private fun body(
        name: String,
        on: UUID = agent.agentId,
        path: String = "/etc",
    ) = """{"name":"$name","agentId":"$on","plugin":"files","repositoryName":"qa","config":{"paths":["$path"]}}"""

    private fun source(
        name: String,
        on: UUID = agent.agentId,
    ) = world.api
        .post("/api/v1/sources", admin, body(name, on))
        .json
        .path("id")
        .asString()

    private fun start(source: String) = world.api.post("/api/v1/sources/$source/runs", admin, null)

    private fun card(run: String) = world.api.get("/api/v1/runs/$run", admin).json

    private fun revoke() = world.api.post("/api/v1/agents/${agent.agentId}/revoke", admin, null)

    @Test
    fun `Отзыв агента переводит его активные шаги в lost`() {
        val fake = connected()
        val sources = listOf("running", "dispatched", "queued").map { it to source(it) }
        val runs = sources.associate { (name, source) -> name to start(source).json.path("id").asString() }
        val steps = runs.keys.map { fake.nextStep() }
        fake.progress(steps.first())
        eventually { assertEquals("running", card(runs.getValue("running")).path("status").asString()) }
        world.forceRun(UUID.fromString(runs.getValue("queued")), "queued")

        revoke()

        for (run in runs.values) {
            val card = card(run)
            assertEquals("failed", card.path("status").asString(), run)
            assertEquals("agent revoked", card.path("message").asString())
            assertEquals(
                "lost",
                card
                    .path("steps")
                    .get(0)
                    .path("status")
                    .asString(),
            )
            assertEquals(
                "agent revoked",
                card
                    .path("steps")
                    .get(0)
                    .path("message")
                    .asString(),
            )
        }
        for ((_, source) in sources) {
            assertEquals(
                204,
                world.api.send("DELETE", "/api/v1/sources/$source", admin).status,
            )
        }
    }

    @Test
    fun `Результат, пришедший до закрытия стрима, сохраняется как обычно`() {
        val fake = connected()
        val source = source("etc")
        val run = start(source).json.path("id").asString()
        val step = fake.nextStep()
        fake.progress(step)
        fake.result(
            step,
            StepStatus.STEP_STATUS_SUCCEEDED,
            backup = FakeAgent.backup(repository = "r0".padEnd(64, '0')),
        )
        eventually { assertEquals("succeeded", card(run).path("status").asString()) }

        revoke()

        assertEquals(
            "succeeded",
            card(run)
                .path("steps")
                .get(0)
                .path("status")
                .asString(),
        )
        val snapshots = world.api.get("/api/v1/sources/$source/snapshots", admin).json
        assertEquals(1, snapshots.path("items").size())
    }

    @Test
    fun `Одновременные отзыв агента и запуск его источника не оставляют запуска, ждущего вечно`() {
        val source = source("etc")
        val pool = Executors.newFixedThreadPool(2)
        val go = CountDownLatch(1)
        val revocation =
            pool.submit(
                Callable {
                    go.await()
                    revoke()
                },
            )
        val run =
            pool.submit(
                Callable {
                    go.await()
                    start(source)
                },
            )
        go.countDown()
        revocation.get(RACE_SECONDS, TimeUnit.SECONDS)
        val started = run.get(RACE_SECONDS, TimeUnit.SECONDS)
        pool.shutdown()

        if (started.status == 409) {
            assertEquals("agent_revoked", started.code)
        } else {
            assertEquals(201, started.status)
            assertEquals(
                "lost",
                card(started.json.path("id").asString())
                    .path("steps")
                    .get(0)
                    .path("status")
                    .asString(),
            )
        }
        assertEquals(
            0,
            world.jdbc.queryForObject(
                "select count(*) from runs where status in ('queued', 'dispatched', 'running')",
                Int::class.java,
            ),
        )
        assertEquals(204, world.api.send("DELETE", "/api/v1/sources/$source", admin).status)
    }

    @Test
    fun `Изменение источника при активном запуске разрешено, запуск идёт со старым конфигом`() {
        val source = source("etc")
        val run = start(source).json.path("id").asString()
        assertEquals("queued", card(run).path("status").asString())

        val response = world.api.send("PUT", "/api/v1/sources/$source", admin, body("etc", path = "/var/www"))

        assertEquals(200, response.status)
        val sent = connected().nextRunStep()
        assertEquals(mapper.readTree("""{"paths":["/etc"]}"""), mapper.readTree(sent.configJson))
    }

    @Test
    fun `Запуск после изменения источника идёт с новым конфигом`() {
        val fake = connected()
        val source = source("etc")
        val first = start(source).json.path("id").asString()
        fake.nextStep()
        world.api.send("PUT", "/api/v1/sources/$source", admin, body("etc", path = "/var/www"))
        world.forceRun(UUID.fromString(first), "succeeded")

        start(source)

        assertEquals(mapper.readTree("""{"paths":["/var/www"]}"""), mapper.readTree(fake.nextRunStep().configJson))
        assertTrue(world.count("run_steps", tenant) == 2)
    }

    // --- The agent row's lock orders a revocation and a run being started, deterministically

    /** Holds [mode] lock on the agent's row in an open transaction until [release]; the server's writers wait. */
    private fun <T> holdingAgentRow(
        mode: String,
        release: (java.sql.Connection) -> Unit,
        whileHeld: () -> T,
    ): T =
        world.jdbc.dataSource!!.connection.use { blocker ->
            blocker.autoCommit = false
            blocker.prepareStatement("select id from agents where id = ? $mode").use {
                it.setObject(1, agent.agentId)
                it.executeQuery().close()
            }
            val result = whileHeld()
            release(blocker)
            blocker.commit()
            result
        }

    /** Waits until [count] statements that begin with [verb] wait for a lock on the agents table. */
    private fun awaitLockWaiters(
        count: Int,
        verb: String = "select",
    ) {
        val sql =
            "select count(*) from pg_stat_activity where wait_event_type = 'Lock' and query like '%agents%' " +
                "and lower(query) like '$verb%'"
        val deadline = System.nanoTime() + TimeUnit.SECONDS.toNanos(RACE_SECONDS)
        while (world.jdbc.queryForObject(sql, Int::class.java)!! < count) {
            check(System.nanoTime() < deadline) { "nobody waited for the agent's row lock" }
            Thread.sleep(LOCK_POLL_MILLIS)
        }
    }

    @Test
    fun `a run being started waits for a revocation holding the agent and is refused after it`() {
        val source = source("etc")
        val pool = Executors.newSingleThreadExecutor()
        val response =
            holdingAgentRow(
                "for no key update",
                {
                    it.createStatement().use { s ->
                        s.executeUpdate("update agents set revoked_at = now() where id = '${agent.agentId}'")
                    }
                },
            ) {
                val started = pool.submit(Callable { start(source) })
                awaitLockWaiters(1)
                started
            }.get(RACE_SECONDS, TimeUnit.SECONDS)
        pool.shutdown()

        assertEquals(409, response.status)
        assertEquals("agent_revoked", response.code)
        assertEquals(0, world.count("runs", tenant))
    }

    @Test
    fun `a revocation waits for a run being started and then loses its step`() {
        val source = source("etc")
        val run = start(source).json.path("id").asString()
        val pool = Executors.newSingleThreadExecutor()
        val response =
            holdingAgentRow("for share", {}) {
                val revoked = pool.submit(Callable { revoke() })
                // The revocation asks for the row lock first (a select); an update that waited would be too late.
                awaitLockWaiters(1, "select")
                revoked
            }.get(RACE_SECONDS, TimeUnit.SECONDS)
        pool.shutdown()

        assertEquals(200, response.status)
        assertEquals(
            "lost",
            card(run)
                .path("steps")
                .get(0)
                .path("status")
                .asString(),
        )
        assertEquals("agent revoked", card(run).path("message").asString())
    }

    private companion object {
        const val LOCK_POLL_MILLIS = 20L
    }
}
