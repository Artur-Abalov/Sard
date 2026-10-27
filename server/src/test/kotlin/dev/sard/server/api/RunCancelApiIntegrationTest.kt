// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import dev.sard.proto.agent.v1.StepStatus
import dev.sard.server.agents.dispatch.ReconciledHellos
import dev.sard.server.agents.dispatch.StepDispatcher
import dev.sard.server.enrollment.Enrollment
import dev.sard.server.enrollment.EnrollmentTokens
import dev.sard.server.pki.CertificateAuthority
import dev.sard.server.pki.MovableClock
import dev.sard.server.registration.Registration
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.beans.factory.annotation.Value
import org.springframework.boot.test.web.server.LocalServerPort
import org.springframework.jdbc.core.JdbcTemplate
import tools.jackson.databind.JsonNode
import tools.jackson.databind.ObjectMapper
import kotlin.test.AfterTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

/** Rules of J1a: "Запуск в очереди отменяется сразу", "Отданный агенту запуск отменяет агент" and the 409s. */
@RestApiTest
class RunCancelApiIntegrationTest(
    @Autowired ca: CertificateAuthority,
    @Autowired enrollment: Enrollment,
    @Autowired tokens: EnrollmentTokens,
    @Autowired registration: Registration,
    @Autowired private val jdbc: JdbcTemplate,
    @Autowired private val clock: MovableClock,
    @Autowired private val mapper: ObjectMapper,
    @Autowired private val hellos: ReconciledHellos,
    @Autowired private val dispatcher: StepDispatcher,
    @LocalServerPort port: Int,
    @Value("\${local.grpc.server.port}") grpc: Int,
) {
    private val world = RestWorld(ca, enrollment, tokens, registration, jdbc, clock, mapper, port, grpc)
    private val tenant = world.tenant()
    private val admin = world.admin(tenant)
    private val agent = world.agent(tenant)

    @AfterTest
    fun `drop the tenants`() = world.close()

    private fun source(name: String = "etc"): String =
        world.api
            .post("/api/v1/sources", admin, sourceJson(name, agent.agentId))
            .json
            .path("id")
            .asString()

    private fun start(source: String) = world.api.post("/api/v1/sources/$source/runs", admin, null)

    private fun cancel(
        run: String,
        session: ApiSession? = admin,
    ) = world.api.post("/api/v1/runs/$run/cancel", session, null)

    private fun card(run: String): JsonNode = world.api.get("/api/v1/runs/$run", admin).json

    @Test
    fun `Отмена запуска в очереди закрывает его без команды агенту`() {
        val source = source()
        val run = start(source).json.path("id").asString()
        clock.now = T0.plusSeconds(7)

        val response = cancel(run)

        assertEquals(200, response.status)
        val json = response.json
        assertEquals(
            listOf("cancelled", "cancelled"),
            listOf(json.path("status").asString(), json.path("outcome").asString()),
        )
        val cancellation = json.path("cancellation")
        assertEquals(
            listOf("admin", T0.plusSeconds(7).toString(), "cancelled"),
            listOf("requestedBy", "requestedAt", "result").map { cancellation.path(it).asString() },
        )
        val step = json.path("steps").single()
        assertEquals(
            listOf("cancelled", "cancelled before dispatch", T0.plusSeconds(7).toString()),
            listOf("status", "message", "finishedAt").map { step.path(it).asString() },
        )
        assertTrue(step.path("dispatchedAt").isNull)
        assertEquals(201, start(source).status)
    }

    /** The agent of the tenant, connected and greeted; its steps and what it is told arrive through it. */
    private fun connected(): FakeAgent {
        val fake = FakeAgent(world.connect(agent).also { it.hello() })
        hellos.await(agent.agentId)
        return fake
    }

    /** A run of a new source that the connected [fake] accepted: the server sees it running. */
    private fun running(
        fake: FakeAgent,
        name: String = "etc",
    ): Pair<String, String> {
        val source = source(name)
        val run = start(source).json.path("id").asString()
        val step = fake.nextStep()
        fake.progress(step)
        eventually { assertEquals("running", card(run).path("status").asString()) }
        return run to step
    }

    @Test
    fun `Отмена выполняемого запуска посылает агенту CancelStep, источник занят до его ответа`() {
        val fake = connected()
        val (run, step) = running(fake)

        val response = cancel(run)

        assertEquals(200, response.status)
        assertEquals(step, fake.nextCancel())
        val json = response.json
        assertEquals(
            listOf("running", "cancelling", "pending"),
            listOf(
                json.path("status").asString(),
                json.path("outcome").asString(),
                json.path("cancellation").path("result").asString(),
            ),
        )
        val again = start(json.path("sourceId").asString())
        assertEquals(409, again.status)
        assertEquals("run_active", again.code)
        assertEquals(run, again.json.path("activeRunId").asString())
    }

    @Test
    fun `Подтверждение агента закрывает запуск как отменённый и освобождает источник`() {
        val fake = connected()
        val (run, step) = running(fake)
        cancel(run)
        fake.result(step, StepStatus.STEP_STATUS_CANCELLED, "cancelled by the server")

        val json = eventually { card(run).also { assertEquals("cancelled", it.path("status").asString()) } }

        assertEquals("cancelled", json.path("outcome").asString())
        assertEquals("cancelled", json.path("cancellation").path("result").asString())
        assertEquals(201, start(json.path("sourceId").asString()).status)
    }

    @Test
    fun `Итог агента после запроса отмены становится итогом запуска`() {
        val cases =
            listOf(
                Triple(StepStatus.STEP_STATUS_SUCCEEDED, "succeeded", "succeeded"),
                Triple(StepStatus.STEP_STATUS_FAILED, "failed", "failed"),
                Triple(StepStatus.STEP_STATUS_TIMED_OUT, "failed", "timed_out"),
            )
        val fake = connected()
        for ((index, case) in cases.withIndex()) {
            val (agentStatus, status, outcome) = case
            val (run, step) = running(fake, "etc-$index")
            cancel(run)
            val backup = FakeAgent.backup().takeIf { agentStatus == StepStatus.STEP_STATUS_SUCCEEDED }
            fake.result(step, agentStatus, "", backup)

            val json = eventually { card(run).also { assertEquals(status, it.path("status").asString()) } }

            assertEquals(outcome, json.path("outcome").asString(), "$agentStatus")
            assertEquals("finished_first", json.path("cancellation").path("result").asString(), "$agentStatus")
        }
    }

    @Test
    fun `Повторная отмена отвечает 200 и не меняет запись отмены`() {
        val fake = connected()
        val (run, _) = running(fake)
        clock.now = T0.plusSeconds(3)
        val first = cancel(run).json.path("cancellation")
        clock.now = T0.plusSeconds(8)

        val second = cancel(run)

        assertEquals(200, second.status)
        assertEquals(first, second.json.path("cancellation"))
        assertEquals(T0.plusSeconds(3).toString(), card(run).path("cancellation").path("requestedAt").asString())
    }

    @Test
    fun `Отмена завершённого запуска отвечает 409 run_finished и ничего не меняет`() {
        val source = source()
        for (status in listOf("succeeded", "failed", "cancelled")) {
            val run = start(source).json.path("id").asString()
            world.forceRun(java.util.UUID.fromString(run), status)

            val response = cancel(run)

            assertEquals(409, response.status, status)
            assertEquals("run_finished", response.code, status)
            assertEquals(status, card(run).path("status").asString())
        }
    }

    @Test
    fun `Отмена запуска проверки отвечает 409 run_not_cancellable до J1f`() {
        val run = start(source()).json.path("id").asString()
        world.forceRun(java.util.UUID.fromString(run), "running")
        jdbc.update("update runs set trigger = 'verification' where id = ?", java.util.UUID.fromString(run))

        val response = cancel(run)

        assertEquals(409, response.status)
        assertEquals("run_not_cancellable", response.code)
        assertEquals("running", card(run).path("status").asString())
    }

    @Test
    fun `Отмена без сессии отвечает 401, с чужим Origin 403, запуск не меняется`() {
        val run = start(source()).json.path("id").asString()

        val anonymous = cancel(run, session = null)
        val strange = mapOf("Origin" to "https://evil.example")
        val foreign = world.api.send("POST", "/api/v1/runs/$run/cancel", admin, null, strange)

        assertEquals(401, anonymous.status)
        assertEquals("unauthenticated", anonymous.code)
        assertEquals(403, foreign.status)
        assertEquals("origin_rejected", foreign.code)
        assertEquals("queued", card(run).path("status").asString())
    }

    @Test
    fun `Отмена запуска другого тенанта отвечает 404`() {
        val run = start(source()).json.path("id").asString()
        val other = world.admin(world.tenant())

        val response = cancel(run, session = other)

        assertEquals(404, response.status)
        assertEquals("not_found", response.code)
        assertEquals("queued", card(run).path("status").asString())
    }

    @Test
    fun `Запись запуска называет, кто и когда отменил, а список даёт outcome`() {
        val fake = connected()
        val (run, _) = running(fake)
        clock.now = T0.plusSeconds(7)
        cancel(run)

        val cancellation = card(run).path("cancellation")
        val listed =
            world.api
                .get("/api/v1/runs", admin)
                .json
                .path("items")
                .single { it.path("id").asString() == run }

        assertEquals("admin", cancellation.path("requestedBy").asString())
        assertEquals(T0.plusSeconds(7).toString(), cancellation.path("requestedAt").asString())
        assertEquals("cancelling", listed.path("outcome").asString())
    }

    /** A cancelled run whose agent went away and did not confirm: the lost window passed and the tick closed it. */
    private fun unconfirmed(fake: FakeAgent): Triple<String, String, String> {
        val (run, step) = running(fake)
        cancel(run)
        jdbc.update(
            "update run_steps set lost_deadline = ? where id = ?",
            java.sql.Timestamp.from(clock.now),
            java.util.UUID.fromString(step),
        )
        dispatcher.tick()
        val json = card(run)
        assertEquals("cancel_unconfirmed", json.path("outcome").asString())
        return Triple(run, step, json.path("sourceId").asString())
    }

    @Test
    fun `Агент не подтвердил отмену - отменён, агент не подтвердил, шаг не lost, источник свободен`() {
        val fake = connected()
        val (run, _, source) = unconfirmed(fake)

        val json = card(run)

        assertEquals(
            listOf("cancelled", "unconfirmed"),
            listOf(json.path("status").asString(), json.path("cancellation").path("result").asString()),
        )
        val step = json.path("steps").single()
        assertEquals("cancelled", step.path("status").asString())
        assertEquals("the agent did not confirm the cancellation", step.path("message").asString())
        assertEquals(201, start(source).status)
    }

    @Test
    fun `Поздний успех бэкапа после неподтверждённой отмены записывает снимок, запуск остаётся отменённым`() {
        val fake = connected()
        val (run, step, source) = unconfirmed(fake)

        fake.result(step, StepStatus.STEP_STATUS_SUCCEEDED, backup = FakeAgent.backup("a1b2c3"))

        val json =
            eventually {
                card(run).also {
                    assertEquals(
                        "succeeded",
                        it
                            .path("steps")
                            .get(0)
                            .path("lateStatus")
                            .asString(),
                    )
                }
            }
        assertEquals("cancel_unconfirmed", json.path("outcome").asString())
        assertEquals(
            "cancelled",
            json
                .path("steps")
                .get(0)
                .path("status")
                .asString(),
        )
        val snapshot =
            world.api
                .get("/api/v1/sources/$source/snapshots", admin)
                .json
                .path("items")
                .single()
        assertEquals(
            listOf("a1b2c3", false, true),
            listOf("snapshotId", "partial", "restorable").map {
                snapshot.path(it).let { node ->
                    if (node.isBoolean) node.asBoolean() else node.asString()
                }
            },
        )
    }

    @Test
    fun `Поздний итог CANCELLED после неподтверждённой отмены сохраняется, статус не меняется`() {
        val fake = connected()
        val (run, step, _) = unconfirmed(fake)

        fake.result(step, StepStatus.STEP_STATUS_CANCELLED, "cancelled by the server")

        val json =
            eventually {
                card(run).also {
                    assertEquals(
                        "cancelled",
                        it
                            .path("steps")
                            .get(0)
                            .path("lateStatus")
                            .asString(),
                    )
                }
            }
        assertEquals("cancel_unconfirmed", json.path("outcome").asString())
    }
}
