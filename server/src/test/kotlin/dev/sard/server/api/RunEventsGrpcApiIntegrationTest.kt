// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import dev.sard.proto.agent.v1.StepStatus
import dev.sard.server.agents.dispatch.ReconciledHellos
import dev.sard.server.agents.stream.TestAgent
import dev.sard.server.enrollment.Enrollment
import dev.sard.server.enrollment.EnrollmentTokens
import dev.sard.server.extension.EventLog
import dev.sard.server.extension.RecordingConfiguration
import dev.sard.server.extension.RunCreated
import dev.sard.server.extension.RunEvent
import dev.sard.server.extension.RunStarted
import dev.sard.server.extension.RunStatusChanged
import dev.sard.server.extension.Subject
import dev.sard.server.pki.CertificateAuthority
import dev.sard.server.pki.MovableClock
import dev.sard.server.registration.Registration
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.beans.factory.annotation.Value
import org.springframework.boot.test.web.server.LocalServerPort
import org.springframework.context.annotation.Import
import org.springframework.jdbc.core.JdbcTemplate
import tools.jackson.databind.ObjectMapper
import java.util.UUID
import kotlin.test.AfterTest
import kotlin.test.BeforeTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNull
import kotlin.test.assertTrue

/** Rule "Запуск публикует «создан», «начат» и каждую смену статуса" (server-core-extensions.feature, @http @grpc). */
@RestApiTest
@Import(RecordingConfiguration::class)
class RunEventsGrpcApiIntegrationTest(
    @Autowired ca: CertificateAuthority,
    @Autowired enrollment: Enrollment,
    @Autowired tokens: EnrollmentTokens,
    @Autowired registration: Registration,
    @Autowired jdbc: JdbcTemplate,
    @Autowired clock: MovableClock,
    @Autowired mapper: ObjectMapper,
    @Autowired private val hellos: ReconciledHellos,
    @Autowired private val log: EventLog,
    @LocalServerPort port: Int,
    @Value("\${local.grpc.server.port}") grpc: Int,
) {
    private val world = RestWorld(ca, enrollment, tokens, registration, jdbc, clock, mapper, port, grpc)
    private val tenant = world.tenant()
    private val admin = world.admin(tenant)
    private val agent = world.agent(tenant)
    private val offline = world.agent(tenant, hostname = "offline")
    private val fake = FakeAgent(world.connect(agent).also { it.hello() })

    init {
        hellos.await(agent.agentId)
    }

    @BeforeTest
    fun `no events yet`() = log.clear()

    @AfterTest
    fun `drop the tenants`() = world.close()

    private fun source(on: TestAgent = agent): String =
        world.api
            .post("/api/v1/sources", admin, sourceJson("s-${UUID.randomUUID()}", on.agentId))
            .json
            .path("id")
            .asString()

    private fun start(source: String) = world.api.post("/api/v1/sources/$source/runs", admin, null)

    private fun eventsOf(run: String): List<RunEvent> = log.of<RunEvent>().filter { it.runId.toString() == run }

    private fun changes(run: String) = eventsOf(run).filterIsInstance<RunStatusChanged>()

    private fun terminal(run: String) =
        eventually {
            val finals = changes(run).filter { it.terminal }
            assertEquals(1, finals.size, changes(run).toString())
            finals.single()
        }

    @Test
    fun `Ручной бэкап даёт событие создан с полями запуска`() {
        val source = source()

        val run = start(source).json.path("id").asString()

        val created = eventsOf(run).filterIsInstance<RunCreated>().single()
        assertEquals("backup", created.kind)
        assertEquals("manual", created.trigger)
        assertEquals(source, created.sourceId.toString())
        assertEquals(Subject.ADMINISTRATOR, created.actor)
        assertEquals(tenant, created.tenantId)
    }

    @Test
    fun `Создание запуска не даёт ни начат ни смены статуса`() {
        val run = start(source(offline)).json.path("id").asString()

        assertEquals(
            "queued",
            world.api
                .get("/api/v1/runs/$run", admin)
                .json
                .path("status")
                .asString(),
        )
        assertEquals(listOf("run.created"), eventsOf(run).map { it.type })
    }

    @Test
    fun `Отклонённый запрос запуска не даёт событий`() {
        val source = source(offline)
        start(source)
        log.clear()

        val again = start(source)

        assertEquals(409, again.status, again.toString())
        assertEquals("run_active", again.code)
        assertEquals(emptyList(), log.of<RunEvent>())
    }

    @Test
    fun `Каждая смена статуса запуска даёт событие с from to и terminal`() {
        val source = source()
        val run = start(source).json.path("id").asString()
        val step = fake.nextStep()
        fake.progress(step)
        eventually {
            assertEquals(
                "running",
                world.api
                    .get("/api/v1/runs/$run", admin)
                    .json
                    .path("status")
                    .asString(),
            )
        }

        fake.result(step, StepStatus.STEP_STATUS_SUCCEEDED, backup = FakeAgent.backup("a1b2c3", unreadableCount = 0))
        val last = terminal(run)

        val steps = changes(run).map { Triple(it.from, it.to, it.terminal) }
        assertEquals(
            listOf(
                Triple("queued", "dispatched", false),
                Triple("dispatched", "running", false),
                Triple("running", "succeeded", true),
            ),
            steps,
        )
        assertEquals("succeeded", last.to)
        assertEquals(1, eventsOf(run).filterIsInstance<RunStarted>().size)
        val order = eventsOf(run).map { it.type }
        assertTrue(order.indexOf("run.created") < order.indexOf("run.started"), order.toString())
        assertEquals("run.status_changed", order.last())
    }

    @Test
    fun `Неудачный бэкап даёт итоговую смену статуса в failed и никогда в succeeded`() {
        val run = start(source()).json.path("id").asString()
        val step = fake.nextStep()
        fake.progress(step)

        fake.result(step, StepStatus.STEP_STATUS_FAILED, message = "disk gone")

        val last = terminal(run)
        assertEquals("failed", last.to)
        assertTrue(changes(run).none { it.to == "succeeded" })
    }

    @Test
    fun `После итоговой смены статуса событий о запуске больше нет`() {
        val run = start(source()).json.path("id").asString()
        val step = fake.nextStep()
        fake.progress(step)
        fake.result(step, StepStatus.STEP_STATUS_FAILED, message = "disk gone")
        terminal(run)
        val before = eventsOf(run).size

        fake.result(step, StepStatus.STEP_STATUS_FAILED, message = "disk gone")
        fake.progress(step)
        Thread.sleep(SETTLE_MILLIS)

        assertEquals(before, eventsOf(run).size)
    }

    @Test
    fun `Запуск, отменённый в очереди, даёт смену статуса в cancelled без начат`() {
        val run = start(source(offline)).json.path("id").asString()
        log.clear()

        val cancelled = world.api.post("/api/v1/runs/$run/cancel", admin, null)

        assertEquals(200, cancelled.status, cancelled.toString())
        val change = changes(run).single()
        assertEquals(Triple("queued", "cancelled", true), Triple(change.from, change.to, change.terminal))
        assertNull(eventsOf(run).filterIsInstance<RunStarted>().firstOrNull())
    }

    private companion object {
        const val SETTLE_MILLIS = 500L
    }
}
