// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import dev.sard.proto.agent.v1.Action
import dev.sard.proto.agent.v1.LogLevel
import dev.sard.proto.agent.v1.RestoreOutput
import dev.sard.proto.agent.v1.RunStep
import dev.sard.proto.agent.v1.StepPhase
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
import tools.jackson.databind.JsonNode
import tools.jackson.databind.ObjectMapper
import java.util.UUID
import kotlin.test.AfterTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNotEquals
import kotlin.test.assertTrue

private const val QUIET_MILLIS = 300L
private const val TARGET = "/var/lib/sard-agent/restore/etc%20data/20261010T030000Z"

/** R2a: the restore run as an agent really reports it over gRPC (docs/specs/server/console-restore.feature). */
@RestApiTest
class RestoresGrpcApiIntegrationTest(
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
    private val w = RestoreWorld(world, tenant, admin)
    private val source = w.source()

    init {
        w.snapshot(source)
    }

    @AfterTest
    fun `drop the tenants`() = world.close()

    private fun connect(who: dev.sard.server.agents.stream.TestAgent = w.a): FakeAgent =
        FakeAgent(world.connect(who).also { it.hello() }).also { hellos.await(who.agentId) }

    /** A restore run on [fake]'s agent that the agent has received: the run, the step id and the command. */
    private class Started(
        val run: String,
        val step: String,
        val command: RunStep,
    )

    private fun started(
        fake: FakeAgent,
        agent: dev.sard.server.agents.stream.TestAgent = w.a,
        config: String = """{"destination": "etc%20data/20261010T030000Z"}""",
    ): Started {
        val response = w.restore(source, restoreBody(agent.agentId, config))
        assertEquals(201, response.status, response.toString())
        val command = fake.nextRunStep()
        return Started(response.json.path("id").asString(), command.commandId, command)
    }

    private fun card(run: String): JsonNode = world.api.get("/api/v1/runs/$run", admin).json

    private fun stepOf(run: String): JsonNode = card(run).path("steps").get(0)

    private fun restored(
        target: String = TARGET,
        files: Long = 2,
        bytes: Long = 4,
    ): RestoreOutput =
        RestoreOutput
            .newBuilder()
            .setTarget(target)
            .setFilesRestored(files)
            .setBytesRestored(bytes)
            .setOwnersRestored(false)
            .build()

    @Test
    fun `Агент получает команду только с именами`() {
        val fake = connect(w.b)

        val started = started(fake, w.b)

        val command = started.command
        assertEquals(Action.ACTION_RESTORE, command.action)
        assertEquals(listOf("files", "copy", SF), listOf(command.plugin, command.repositoryName, command.snapshotId))
        assertEquals("""{"destination":"etc%20data/20261010T030000Z"}""", command.configJson.replace(" ", ""))
        assertEquals(
            mapOf("sard.source" to source, "sard.run" to started.run, "sard.step" to started.step),
            command.tagsMap,
        )
        assertFalse(command.toString().contains("/var/lib/sard-agent"), "the path of the target is not sent")
        assertEquals(started.step, stepOf(started.run).path("id").asString())
    }

    @Test
    fun `Восстановление на офлайн-агент ждёт подключения`() {
        val response = w.restore(source, restoreBody(w.b.agentId))
        assertEquals("queued", response.json.path("status").asString())
        Thread.sleep(QUIET_MILLIS)
        assertEquals("queued", card(response.json.path("id").asString()).path("status").asString())

        val fake = connect(w.b)

        assertEquals(Action.ACTION_RESTORE, fake.nextRunStep().action)
    }

    @Test
    fun `Ход восстановления виден в шаге`() {
        val fake = connect()
        val started = started(fake)

        fake.progress(started.step, StepPhase.STEP_PHASE_RESTORING, bytes = 100, total = 200, files = 1, filesTotal = 2)

        val step = eventually { stepOf(started.run).also { assertEquals(100, it.path("bytesProcessed").asLong()) } }
        assertEquals("running", step.path("status").asString())
        assertEquals("restoring", step.path("phase").asString())
        assertEquals(
            listOf(200L, 1L, 2L),
            listOf("bytesTotal", "filesProcessed", "filesTotal").map { step.path(it).asLong() },
        )
    }

    @Test
    fun `Успешный итог files называет путь, число файлов и объём`() {
        val fake = connect()
        val started = started(fake)
        fake.progress(started.step, StepPhase.STEP_PHASE_RESTORING)

        fake.result(started.step, StepStatus.STEP_STATUS_SUCCEEDED, restore = restored())

        val card = eventually { card(started.run).also { assertEquals("succeeded", it.path("status").asString()) } }
        val restore = card.path("steps").get(0).path("restore")
        assertEquals(TARGET, restore.path("target").asString())
        assertEquals(listOf(2L, 4L), listOf("filesRestored", "bytesRestored").map { restore.path(it).asLong() })
        assertFalse(restore.path("ownersRestored").asBoolean(true))
        assertTrue(restore.path("leftover").isNull)
        assertTrue(listOf("database", "tablesRestored", "rolesRestored").all { restore.path(it).isNull })
    }

    @Test
    fun `Успех, о котором агент сообщил после перезапуска, записывается как успех`() {
        val fake = connect()
        val started = started(fake)

        fake.result(
            started.step,
            StepStatus.STEP_STATUS_SUCCEEDED,
            "agent restarted after the restore completed",
            restore = restored(),
        )

        val card = eventually { card(started.run).also { assertEquals("succeeded", it.path("status").asString()) } }
        val restore = card.path("steps").get(0).path("restore")
        assertEquals(TARGET, restore.path("target").asString())
        assertEquals(2L, restore.path("filesRestored").asLong())
        assertTrue(restore.path("leftover").isNull)
    }

    @Test
    fun `Неудачное восстановление называет стадию и причину и ничего не оставляет`() {
        val fake = connect()
        val started = started(fake)
        fake.progress(started.step, StepPhase.STEP_PHASE_RESTORING)
        val message = "TARGET_WRITE (restore): no space left on device; nothing was restored"

        fake.result(started.step, StepStatus.STEP_STATUS_FAILED, message)

        val card = eventually { card(started.run).also { assertEquals("failed", it.path("status").asString()) } }
        assertEquals(message, card.path("message").asString())
        val step = card.path("steps").get(0)
        assertEquals("restoring", step.path("phase").asString())
        assertTrue(step.path("restore").isNull)
    }

    @Test
    fun `Неудачный откат виден как отдельный исход с тем, что осталось`() {
        val fake = connect()
        val started = started(fake)
        val staging = "/var/lib/sard-agent/restore/.sard-staging/${started.step}"
        val left = RestoreOutput.newBuilder().setLeftover(staging).build()

        fake.result(started.step, StepStatus.STEP_STATUS_FAILED, "CLEANUP_FAILED (rollback): boom", restore = left)

        val step = eventually { stepOf(started.run).also { assertEquals("failed", it.path("status").asString()) } }
        val restore = step.path("restore")
        assertEquals("/var/lib/sard-agent/restore/.sard-staging/${started.step}", restore.path("leftover").asString())
        assertTrue(listOf("target", "filesRestored", "bytesRestored", "ownersRestored").all { restore.path(it).isNull })
    }

    @Test
    fun `Неудачный результат с полями итога - недопустимый результат`() {
        val fake = connect()
        val started = started(fake)

        fake.result(started.step, StepStatus.STEP_STATUS_FAILED, "boom", restore = restored())

        val card = eventually { card(started.run).also { assertEquals("failed", it.path("status").asString()) } }
        assertTrue(card.path("message").asString().startsWith("invalid result"), card.path("message").asString())
        assertTrue(
            card
                .path("steps")
                .get(0)
                .path("restore")
                .isNull,
        )
    }

    @Test
    fun `Вывод бэкапа в шаге восстановления - недопустимый результат`() {
        val fake = connect()
        val started = started(fake)

        fake.result(started.step, StepStatus.STEP_STATUS_SUCCEEDED, backup = FakeAgent.backup(repository = RID))

        val card = eventually { card(started.run).also { assertEquals("failed", it.path("status").asString()) } }
        assertTrue(card.path("message").asString().startsWith("invalid result"))
        assertEquals(1, world.count("snapshots", tenant), "a restore makes no snapshot")
    }

    @Test
    fun `Потерянное восстановление честно помечено и не отправляется повторно`() {
        val fake = connect()
        val started = started(fake)
        fake.progress(started.step, StepPhase.STEP_PHASE_RESTORING)
        eventually { assertEquals("running", stepOf(started.run).path("status").asString()) }

        world.forceRun(UUID.fromString(started.run), "lost", "agent lost the step")

        val card = card(started.run)
        assertEquals("failed", card.path("status").asString())
        assertEquals(
            "lost",
            card
                .path("steps")
                .get(0)
                .path("status")
                .asString(),
        )
        assertEquals("agent lost the step", card.path("message").asString())
    }

    @Test
    fun `Поздний успех потерянного восстановления сохраняется и виден`() {
        val fake = connect()
        val started = started(fake)
        fake.progress(started.step, StepPhase.STEP_PHASE_RESTORING)
        eventually { assertEquals("running", stepOf(started.run).path("status").asString()) }
        world.forceRun(UUID.fromString(started.run), "lost", "agent lost the step")

        fake.result(started.step, StepStatus.STEP_STATUS_SUCCEEDED, restore = restored(TARGET))

        val step =
            eventually {
                stepOf(started.run).also { assertEquals(TARGET, it.path("restore").path("target").asString()) }
            }
        assertEquals("lost", step.path("status").asString())
        assertEquals("failed", card(started.run).path("status").asString())
        assertEquals(2, step.path("restore").path("filesRestored").asLong())
    }

    @Test
    fun `Отзыв агента теряет его восстановление`() {
        val fake = connect(w.b)
        val started = started(fake, w.b)
        fake.progress(started.step, StepPhase.STEP_PHASE_RESTORING)
        eventually { assertEquals("running", stepOf(started.run).path("status").asString()) }

        w.revoke(w.b)

        val card = card(started.run)
        assertEquals("failed", card.path("status").asString())
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

    @Test
    fun `Повтор - новый запуск с тем же запросом`() {
        val fake = connect()
        val first = started(fake)
        world.forceRun(UUID.fromString(first.run), "lost", "agent lost the step")
        val config = card(first.run).path("restore").path("config").toString()

        val again = w.restore(source, restoreBody(w.a.agentId, config))

        assertEquals(201, again.status)
        assertNotEquals(first.run, again.json.path("id").asString())
        assertEquals("failed", card(first.run).path("status").asString())
        assertEquals("restore", again.json.path("kind").asString())
    }

    @Test
    fun `Лог шага восстановления доступен`() {
        val fake = connect()
        val started = started(fake)
        fake.progress(started.step, StepPhase.STEP_PHASE_RESTORING)
        fake.log(started.step, listOf(FakeAgent.line("password is [REDACTED]", LogLevel.LOG_LEVEL_WARN, T0)))

        val lines =
            eventually {
                world.api
                    .get("/api/v1/runs/${started.run}/steps/${started.step}/logs", admin)
                    .json
                    .path("items")
                    .also { assertEquals(1, it.size()) }
            }

        assertEquals("password is [REDACTED]", lines.get(0).path("text").asString())
        assertEquals("warn", lines.get(0).path("level").asString())
    }
}
