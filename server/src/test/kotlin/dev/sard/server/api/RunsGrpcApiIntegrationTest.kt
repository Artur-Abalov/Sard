// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import dev.sard.proto.agent.v1.LogLevel
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
import kotlin.test.assertTrue

private val REPOSITORY_ID = "r0".padEnd(64, '0')

/** Rules "Список и карточка запусков ..." and "Снимки источника" with an agent that really reports over gRPC. */
@RestApiTest
class RunsGrpcApiIntegrationTest(
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
    private val fake = FakeAgent(world.connect(agent).also { it.hello() })

    init {
        hellos.await(agent.agentId)
    }

    @AfterTest
    fun `drop the tenants`() = world.close()

    private fun source(name: String = "etc"): String {
        val body = sourceJson(name, agent.agentId)
        return world.api
            .post("/api/v1/sources", admin, body)
            .json
            .path("id")
            .asString()
    }

    /** A run started now, with the id of its step as the agent got it. */
    private class Started(
        val source: String,
        val run: String,
        val step: String,
    )

    private fun started(name: String = "etc"): Started {
        val source = source(name)
        val run =
            world.api
                .post("/api/v1/sources/$source/runs", admin, null)
                .json
                .path("id")
                .asString()
        return Started(source, run, fake.nextStep())
    }

    private fun card(run: String): JsonNode = world.api.get("/api/v1/runs/$run", admin).json

    private fun stepOf(run: String): JsonNode = card(run).path("steps").get(0)

    private fun snapshots(source: String) =
        world.api
            .get("/api/v1/sources/$source/snapshots", admin)
            .json
            .path("items")

    @Test
    fun `Карточка идущего шага показывает фазу и объёмы`() {
        val started = started()
        fake.progress(started.step, StepPhase.STEP_PHASE_UPLOADING, bytes = 100, total = 1000)

        val step = eventually { stepOf(started.run).also { assertEquals(100, it.path("bytesProcessed").asLong()) } }

        assertEquals("running", step.path("status").asString())
        assertEquals("uploading", step.path("phase").asString())
        assertEquals(100, step.path("bytesProcessed").asLong())
        assertEquals(1000, step.path("bytesTotal").asLong())
        assertTrue(step.path("message").isNull)
        assertEquals(started.step, step.path("id").asString())
    }

    @Test
    fun `Неизвестный объём показан как null`() {
        val started = started()
        fake.progress(started.step, total = 0, bytes = 5)

        val step = eventually { stepOf(started.run).also { assertEquals(5, it.path("bytesProcessed").asLong()) } }

        assertTrue(step.path("bytesTotal").isNull)
    }

    @Test
    fun `Карточка шага показывает счётчики файлов`() {
        val started = started()
        fake.progress(started.step, files = 6, filesTotal = 7)

        val step = eventually { stepOf(started.run).also { assertEquals(6, it.path("filesProcessed").asLong()) } }

        assertEquals(7, step.path("filesTotal").asLong())
    }

    @Test
    fun `Неизвестные счётчики файлов показаны как null`() {
        val started = started()
        fake.progress(started.step, bytes = 5, files = 0, filesTotal = 0)

        val step = eventually { stepOf(started.run).also { assertEquals(5, it.path("bytesProcessed").asLong()) } }

        assertTrue(step.path("filesTotal").isNull)
    }

    @Test
    fun `Успешный шаг показывает вывод бэкапа`() {
        val started = started()
        fake.progress(started.step)
        fake.result(
            started.step,
            StepStatus.STEP_STATUS_SUCCEEDED,
            backup = FakeAgent.backup("a1b2c3", 1000, 10, REPOSITORY_ID),
        )

        val card = eventually { card(started.run).also { assertEquals("succeeded", it.path("status").asString()) } }

        val backup = card.path("steps").get(0).path("backup")
        assertEquals(
            listOf("a1b2c3", 1000L, 10L, REPOSITORY_ID, false),
            listOf(
                backup.path("snapshotId").asString(),
                backup.path("totalBytes").asLong(),
                backup.path("addedBytes").asLong(),
                backup.path("repositoryId").asString(),
                backup.path("partial").asBoolean(true),
            ),
        )
    }

    @Test
    fun `Причина ошибки шага показана как есть`() {
        val cases =
            listOf(
                Triple(StepStatus.STEP_STATUS_FAILED, "restic exited 1", "restic exited 1") to "failed",
                Triple(StepStatus.STEP_STATUS_REJECTED, "unknown plugin files", "unknown plugin files") to "rejected",
                Triple(StepStatus.STEP_STATUS_TIMED_OUT, "deadline exceeded", "deadline exceeded") to "timed_out",
                Triple(StepStatus.STEP_STATUS_SUCCEEDED, "", "invalid result: a succeeded backup without its output") to
                    "failed",
            )
        for ((index, case) in cases.withIndex()) {
            val (result, stepStatus) = case
            val started = started("etc-$index")
            fake.progress(started.step)
            fake.result(started.step, result.first, result.second)

            val card = eventually { card(started.run).also { assertEquals("failed", it.path("status").asString()) } }

            assertEquals(
                stepStatus,
                card
                    .path("steps")
                    .get(0)
                    .path("status")
                    .asString(),
            )
            assertEquals(result.third, card.path("message").asString())
            assertEquals(
                result.third,
                card
                    .path("steps")
                    .get(0)
                    .path("message")
                    .asString(),
            )
        }
    }

    @Test
    fun `Неуспешный шаг с корректным выводом показывает вывод и снимок`() {
        val started = started()
        fake.progress(started.step)
        fake.result(
            started.step,
            StepStatus.STEP_STATUS_FAILED,
            "2 files unreadable",
            FakeAgent.backup("a1b2c3", repository = REPOSITORY_ID),
        )

        val card = eventually { card(started.run).also { assertEquals("failed", it.path("status").asString()) } }

        val step = card.path("steps").get(0)
        assertEquals("2 files unreadable", step.path("message").asString())
        assertEquals("a1b2c3", step.path("backup").path("snapshotId").asString())
        assertEquals(REPOSITORY_ID, step.path("backup").path("repositoryId").asString())
        assertTrue(step.path("backup").path("partial").asBoolean())
        val snapshot = snapshots(started.source).get(0)
        assertEquals(
            listOf("a1b2c3", REPOSITORY_ID, started.step, true),
            listOf(
                snapshot.path("snapshotId").asString(),
                snapshot.path("repositoryId").asString(),
                snapshot.path("stepId").asString(),
                snapshot.path("partial").asBoolean(),
            ),
        )
    }

    @Test
    fun `Неуспешный шаг без вывода показан без backup`() {
        val started = started()
        fake.progress(started.step)
        fake.result(started.step, StepStatus.STEP_STATUS_FAILED, "boom")

        val card = eventually { card(started.run).also { assertEquals("failed", it.path("status").asString()) } }

        assertTrue(
            card
                .path("steps")
                .get(0)
                .path("backup")
                .isNull,
        )
        assertEquals(0, snapshots(started.source).size())
    }

    @Test
    fun `Поздний вывод потерянного шага показан в карточке`() {
        val started = started()
        fake.progress(started.step)
        eventually { assertEquals("running", stepOf(started.run).path("status").asString()) }
        world.forceRun(UUID.fromString(started.run), "lost", "agent lost the step")

        fake.result(
            started.step,
            StepStatus.STEP_STATUS_SUCCEEDED,
            backup = FakeAgent.backup("a1b2c3", repository = REPOSITORY_ID),
        )

        val step =
            eventually {
                stepOf(
                    started.run,
                ).also { assertEquals("a1b2c3", it.path("backup").path("snapshotId").asString()) }
            }
        assertEquals("lost", step.path("status").asString())
        assertEquals("agent lost the step", step.path("message").asString())
        assertEquals(false, step.path("backup").path("partial").asBoolean(true))
    }

    // --- PL7-2: нечитаемые файлы, причина неполноты, признак неполного запуска

    private var sources = 0

    private fun failedWith(
        output: dev.sard.proto.agent.v1.BackupOutput? = null,
        status: StepStatus = StepStatus.STEP_STATUS_FAILED,
        message: String = "at least one source file could not be read",
    ): Started {
        val started = started("src-${sources++}")
        fake.progress(started.step)
        fake.result(started.step, status, message, output)
        eventually {
            assertEquals(true, card(started.run).path("status").asString() in setOf("failed", "succeeded", "cancelled"))
        }
        return started
    }

    private fun unreadable(
        count: Long,
        vararg paths: String,
    ) = FakeAgent.backup(unreadableCount = count, unreadablePaths = paths.toList())

    private fun backupOf(started: Started): JsonNode = stepOf(started.run).path("backup")

    private fun strings(node: JsonNode): List<String> = (0 until node.size()).map { node.get(it).asString() }

    @Test
    fun `Причину неполноты решает сервер по статусу и числу нечитаемых`() {
        val cases =
            listOf(
                Triple(unreadable(2, "/srv/a", "/srv/b"), 2L, "unreadable_files"),
                Triple(FakeAgent.backup(unreadableCount = 0), 0L, "snapshot_set_incomplete"),
                Triple(FakeAgent.backup(), null, "unknown"),
            )
        for ((index, case) in cases.withIndex()) {
            val started = failedWith(case.first)
            val backup = backupOf(started)

            assertEquals(true, backup.path("partial").asBoolean(), "partial $index")
            assertEquals(case.second, backup.path("unreadableCount").takeIf { it.isNumber }?.asLong(), "count $index")
            assertEquals(case.third, backup.path("incompleteReason").asString(), "reason $index")
            assertEquals(true, backup.has("unreadableCount") && backup.has("incompleteReason"))
        }
        assertEquals(
            listOf("/srv/a", "/srv/b"),
            strings(
                backupOf(
                    failedWith(FakeAgent.backup(unreadableCount = 2, unreadablePaths = listOf("/srv/a", "/srv/b"))),
                ).path("unreadablePaths"),
            ),
        )
    }

    @Test
    fun `Тайм-аут с нулём нечитаемых называет неполный набор снимков`() {
        val started = failedWith(FakeAgent.backup(unreadableCount = 0), StepStatus.STEP_STATUS_TIMED_OUT, "timed out")

        assertEquals("snapshot_set_incomplete", backupOf(started).path("incompleteReason").asString())
    }

    @Test
    fun `Успешный вывод не хранит нечитаемых файлов и причины`() {
        val started = started()
        fake.progress(started.step)
        fake.result(
            started.step,
            StepStatus.STEP_STATUS_SUCCEEDED,
            backup = FakeAgent.backup(unreadableCount = 3, unreadablePaths = listOf("/x")),
        )

        val backup = eventually { backupOf(started).also { assertEquals(false, it.isNull) } }

        assertEquals(
            listOf(false, true, 0, true),
            listOf(
                backup.path("partial").asBoolean(),
                backup.path("unreadableCount").isNull,
                backup.path("unreadablePaths").size(),
                backup.path("incompleteReason").isNull,
            ),
        )
    }

    @Test
    fun `Сообщение шага с нечитаемыми файлами хранится как прислал агент`() {
        val message = "at least one source file could not be read: unreadable paths (2): \"/srv/a\", \"/srv/b\""
        val started = failedWith(unreadable(2, "/srv/a", "/srv/b"), message = message)

        assertEquals(message, stepOf(started.run).path("message").asString())
    }

    @Test
    fun `Больше десяти путей от агента - сервер хранит первые десять`() {
        val paths = (1..12).map { "/p%02d".format(it) }
        val started = failedWith(FakeAgent.backup(unreadableCount = 40, unreadablePaths = paths))

        assertEquals(paths.take(10), strings(backupOf(started).path("unreadablePaths")))
        assertEquals(40, backupOf(started).path("unreadableCount").asLong())
    }

    @Test
    fun `Число меньше числа путей заменяется числом путей`() {
        val started = failedWith(FakeAgent.backup(unreadableCount = 1, unreadablePaths = listOf("/a", "/b", "/c")))

        assertEquals(3, backupOf(started).path("unreadableCount").asLong())
        assertEquals("unreadable_files", backupOf(started).path("incompleteReason").asString())
    }

    @Test
    fun `Путь длиннее 4096 символов обрезается с меткой`() {
        val started = failedWith(FakeAgent.backup(unreadableCount = 1, unreadablePaths = listOf("я".repeat(5000))))

        assertEquals(listOf("я".repeat(4096) + "…"), strings(backupOf(started).path("unreadablePaths")))
    }

    @Test
    fun `Маркер маскирования в пути отдаётся как прислал агент`() {
        val started = failedWith(unreadable(1, "/srv/[REDACTED]/a.txt"))

        assertEquals(listOf("/srv/[REDACTED]/a.txt"), strings(backupOf(started).path("unreadablePaths")))
    }

    @Test
    fun `Поздний неполный вывод потерянного шага называет нечитаемые файлы`() {
        val started = started()
        fake.progress(started.step)
        eventually { assertEquals("running", stepOf(started.run).path("status").asString()) }
        world.forceRun(UUID.fromString(started.run), "lost", "agent lost the step")

        fake.result(
            started.step,
            StepStatus.STEP_STATUS_FAILED,
            "x",
            FakeAgent.backup(unreadableCount = 1, unreadablePaths = listOf("/srv/a")),
        )

        val step = eventually { stepOf(started.run).also { assertEquals(false, it.path("backup").isNull) } }
        assertEquals(
            listOf("lost", true, 1L, "unreadable_files"),
            listOf(
                step.path("status").asString(),
                step.path("backup").path("partial").asBoolean(),
                step.path("backup").path("unreadableCount").asLong(),
                step.path("backup").path("incompleteReason").asString(),
            ),
        )
        assertEquals(true, card(started.run).path("partial").asBoolean())
    }

    @Test
    fun `Причина неполноты снимка совпадает с причиной вывода его шага, а неполный снимок не восстанавливается`() {
        val cases =
            listOf(
                FakeAgent.backup("a1", unreadableCount = 2, unreadablePaths = listOf("/a", "/b")) to "unreadable_files",
                FakeAgent.backup("a2", unreadableCount = 0) to "snapshot_set_incomplete",
                FakeAgent.backup("a3") to "unknown",
            )
        for ((output, reason) in cases) {
            val started = failedWith(output)
            val snapshot = snapshots(started.source).get(0)

            assertEquals(
                listOf(true, reason, false, "partial"),
                listOf(
                    snapshot.path("partial").asBoolean(),
                    snapshot.path("incompleteReason").asString(),
                    snapshot.path("restorable").asBoolean(true),
                    snapshot.path("restoreBlockedReason").asString(),
                ),
            )
        }
    }

    @Test
    fun `У полного снимка причины неполноты нет`() {
        val started = started()
        fake.progress(started.step)
        val whole = FakeAgent.backup("a1b2c3", unreadableCount = 0)
        fake.result(started.step, StepStatus.STEP_STATUS_SUCCEEDED, backup = whole)

        val snapshot = eventually { snapshots(started.source).also { assertEquals(1, it.size()) } }.get(0)

        assertEquals(false, snapshot.path("partial").asBoolean(true))
        assertTrue(snapshot.path("incompleteReason").isNull)
    }

    @Test
    fun `Неполный запуск остаётся failed, partial виден в списке и карточке, провал без снимка не partial`() {
        val incomplete = failedWith(unreadable(2, "/a"))
        val broken = failedWith()

        val listed =
            world.api
                .get("/api/v1/runs?status=failed", admin)
                .json
                .path("items")
                .associate { it.path("id").asString() to it }

        val shown = { run: String -> listed.getValue(run).let { it.path("status").asString() to it.path("partial") } }
        assertEquals("failed", shown(incomplete.run).first)
        assertEquals(true, shown(incomplete.run).second.asBoolean())
        assertEquals("failed", shown(broken.run).first)
        assertEquals(false, shown(broken.run).second.asBoolean(true))
        assertEquals(true, card(incomplete.run).path("partial").asBoolean())
        assertEquals(false, card(broken.run).path("partial").asBoolean(true))
    }

    @Test
    fun `Снимок источника показывает репозиторий, идентификаторы и объёмы`() {
        val started = started()
        fake.progress(started.step)
        fake.result(
            started.step,
            StepStatus.STEP_STATUS_SUCCEEDED,
            backup = FakeAgent.backup("a1b2c3", 1000, 10, REPOSITORY_ID),
        )

        val snapshot = eventually { snapshots(started.source).also { assertEquals(1, it.size()) } }.get(0)

        assertEquals(
            listOf("a1b2c3", started.source, started.run, started.step, agent.agentId.toString(), "qa", REPOSITORY_ID),
            listOf(
                "snapshotId",
                "sourceId",
                "runId",
                "stepId",
                "agentId",
                "repositoryName",
                "repositoryId",
            ).map { snapshot.path(it).asString() },
        )
        assertEquals(
            listOf(1000L, 10L, false),
            listOf(
                snapshot.path("totalBytes").asLong(),
                snapshot.path("addedBytes").asLong(),
                snapshot.path("partial").asBoolean(),
            ),
        )
        assertTrue(snapshot.path("forgottenAt").isNull)
        assertEquals(T0.toString(), snapshot.path("createdAt").asString())
    }

    // --- Логи шага (the scenarios that need an agent)

    private fun logsOf(
        started: Started,
        query: String = "",
    ) = world.api.get("/api/v1/runs/${started.run}/steps/${started.step}/logs$query", admin).json

    @Test
    fun `Строка лога показывает время агента, уровень и текст`() {
        val started = started()
        fake.progress(started.step)
        fake.log(started.step, listOf(FakeAgent.line("slow disk", LogLevel.LOG_LEVEL_WARN, T0)))

        val line = eventually { logsOf(started).path("items").also { assertEquals(1, it.size()) } }.get(0)

        assertEquals(1, line.path("seq").asLong())
        assertEquals(T0.toString(), line.path("time").asString())
        assertEquals("warn", line.path("level").asString())
        assertEquals("slow disk", line.path("text").asString())
    }

    @Test
    fun `Строка длиннее 8 КиБ показана обрезанной с пометкой`() {
        val started = started()
        fake.progress(started.step)
        fake.log(started.step, listOf(FakeAgent.line("я".repeat(4500))))

        val text =
            eventually {
                logsOf(started).path("items").also { assertEquals(1, it.size()) }
            }.get(0).path("text").asString()

        assertEquals("я".repeat(4096) + "…[truncated]", text)
    }

    @Test
    fun `Усечённый лог шага отмечен в ответе и завершается строкой сервера`() {
        val started = started()
        fake.progress(started.step)
        val line = FakeAgent.line("x".repeat(8192))
        repeat(LINES_OVER_LIMIT / CHUNK) { fake.log(started.step, List(CHUNK) { line }) }

        var seq = 0L
        var json: JsonNode
        do {
            json =
                eventually {
                    logsOf(
                        started,
                        "?afterSeq=$seq&limit=1000",
                    ).also { assertTrue(it.path("truncated").asBoolean()) }
                }
            assertTrue(json.path("truncated").asBoolean())
            seq = json.path("nextAfterSeq").asLong()
        } while (json.path("hasMore").asBoolean())

        val last = json.path("items").list().last()
        assertEquals("warn", last.path("level").asString())
        assertTrue(last.path("time").isNull)
        assertEquals(
            "log truncated at 16777216 bytes; later lines of this step are dropped",
            last.path("text").asString(),
        )
    }

    @Test
    fun `Строки, пришедшие после результата шага, дочитываются тем же курсором`() {
        val started = started()
        fake.progress(started.step)
        fake.log(started.step, listOf(FakeAgent.line("one"), FakeAgent.line("two")))
        fake.result(
            started.step,
            StepStatus.STEP_STATUS_SUCCEEDED,
            backup = FakeAgent.backup(repository = REPOSITORY_ID),
        )
        val read = eventually { logsOf(started).also { assertEquals(2, it.path("items").size()) } }
        eventually { assertEquals("succeeded", card(started.run).path("status").asString()) }
        val cursor = read.path("nextAfterSeq").asLong()

        fake.log(started.step, listOf(FakeAgent.line("a"), FakeAgent.line("b"), FakeAgent.line("c")))

        val more = eventually { logsOf(started, "?afterSeq=$cursor").also { assertEquals(3, it.path("items").size()) } }
        assertEquals(listOf("a", "b", "c"), more.pluck("items", "text"))
    }

    // --- Правило "Сервер хранит логи шагов без повторного маскирования" (A7b)

    private fun storedTexts(started: Started): List<String> =
        world.jdbc
            .queryForList(
                "select text from step_logs where step_id = ? order by seq",
                String::class.java,
                UUID.fromString(started.step),
            ).filterNotNull()

    private fun assertLineKeptAsSent(text: String) {
        val started = started()
        fake.progress(started.step)
        fake.log(started.step, listOf(FakeAgent.line(text)))

        val items = eventually { logsOf(started).path("items").also { assertEquals(1, it.size()) } }

        assertEquals(text, items.get(0).path("text").asString())
        assertEquals(listOf(text), storedTexts(started))
    }

    @Test
    fun `Строка с маркером хранится и отдаётся без изменений`() = assertLineKeptAsSent("Fatal repo-[REDACTED]/config")

    @Test
    fun `Строка, похожая на секрет, хранится без изменений`() =
        assertLineKeptAsSent(
            "AWS_SECRET_ACCESS_KEY=wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY Authorization Basic dXNlcjpwYXNz",
        )

    @Test
    fun `Сообщение результата шага хранится без изменений`() {
        val started = started()
        fake.progress(started.step)
        val message = "open repo-[REDACTED]/config failed"
        fake.result(started.step, StepStatus.STEP_STATUS_FAILED, message)

        val card = eventually { card(started.run).also { assertEquals("failed", it.path("status").asString()) } }

        assertEquals(
            message,
            card
                .path("steps")
                .get(0)
                .path("message")
                .asString(),
        )
    }

    private companion object {
        /** 17 MiB of 8 KiB lines, above the 16 MiB limit of a step's log. */
        const val LINES_OVER_LIMIT = 2200
        const val CHUNK = 100
    }
}
