// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.notify

import dev.sard.server.TestcontainersConfiguration
import dev.sard.server.notify.telegram.BotReply
import dev.sard.server.notify.telegram.FakeBotApi
import dev.sard.server.notify.telegram.TEST_TOKEN
import dev.sard.server.pki.MovableClock
import dev.sard.server.runs.RUNS_NOW
import dev.sard.server.runs.Runs
import dev.sard.server.runs.RunsTenant
import dev.sard.server.runs.RunsTestConfiguration
import dev.sard.server.runs.Sources
import dev.sard.server.runs.StepOutput
import dev.sard.server.runs.StepReport
import dev.sard.server.runs.StepResults
import dev.sard.server.runs.StepState
import dev.sard.server.runs.StepTransitions
import org.junit.jupiter.api.AfterAll
import org.junit.jupiter.api.extension.ExtendWith
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.boot.test.system.CapturedOutput
import org.springframework.boot.test.system.OutputCaptureExtension
import org.springframework.context.annotation.Import
import org.springframework.jdbc.core.JdbcTemplate
import org.springframework.test.context.DynamicPropertyRegistry
import org.springframework.test.context.DynamicPropertySource
import tools.jackson.databind.json.JsonMapper
import java.sql.Timestamp
import java.time.Duration
import java.util.UUID
import kotlin.test.AfterTest
import kotlin.test.BeforeTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertFalse
import kotlin.test.assertTrue

private const val CHAT = "-100777"
private const val CONSOLE = "https://sard.example.com"
private const val REPOSITORY_ID = "5f0c3e2d9a8b7c6d5e4f3a2b1c0d9e8f7a6b5c4d3e2f1a0b9c8d7e6f5a4b3c2d"
private val SNAPSHOT = StepOutput.Backup("4a3b2c1d", 1_610_612_736, 12_897_485, REPOSITORY_ID)
private val JSON = JsonMapper.builder().build()
private val SUCCEEDED = StepReport(StepState.SUCCEEDED, null, SNAPSHOT)
private const val NOT_MANUAL_WORKFLOW =
    "insert into workflows (id, tenant_id, name, definition, created_at, updated_at) " +
        "values (?, ?, 'nightly', '{}'::jsonb, now(), now())"

/**
 * S9b, the @server scenarios: the real formatter reads finished runs back from PostgreSQL and the
 * message reaches a fake Telegram. Every tick is the test's own; the loop is stopped.
 */
@ExtendWith(OutputCaptureExtension::class)
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.NONE,
    properties = [
        "spring.grpc.server.port=0",
        "sard.notify.tick-interval=1h",
        "SARD_TELEGRAM_BOT_TOKEN=$TEST_TOKEN",
        "SARD_TELEGRAM_CHAT_ID=$CHAT",
        "SARD_NOTIFY_LANGUAGE=ru",
        "SARD_CONSOLE_PUBLIC_URL=$CONSOLE",
    ],
)
@Import(TestcontainersConfiguration::class, RunsTestConfiguration::class)
class RunNotificationsIntegrationTest(
    @Autowired private val service: NotificationService,
    @Autowired private val loop: NotificationLoop,
    @Autowired private val sources: Sources,
    @Autowired private val runs: Runs,
    @Autowired private val steps: StepTransitions,
    @Autowired private val results: StepResults,
    @Autowired private val clock: MovableClock,
    @Autowired private val jdbc: JdbcTemplate,
) {
    private val tenant = RunsTenant(jdbc)

    companion object {
        private val fake = FakeBotApi()

        @JvmStatic
        @DynamicPropertySource
        fun botApi(registry: DynamicPropertyRegistry) {
            registry.add("sard.notify.telegram.api-url") { fake.uri.toString() }
        }

        @JvmStatic
        @AfterAll
        fun closeFake() {
            fake.close()
        }
    }

    @BeforeTest
    fun `a tenant with an agent`() {
        loop.stop()
        clock.now = RUNS_NOW
        fake.requests.clear()
        fake.fallback = FakeBotApi.ok()
        tenant.create()
    }

    @AfterTest
    fun `drop the tenant`() {
        jdbc.update("delete from notification_deliveries where tenant_id = ?", tenant.id)
        jdbc.update("delete from snapshots where tenant_id = ?", tenant.id)
        tenant.drop()
    }

    /** A run whose step started at T0 + 5 s and ended at T0 + 2 min 35 s with [report]; null: the step is lost. */
    private fun finished(
        report: StepReport?,
        source: String = "db-main",
        started: Boolean = true,
    ): UUID {
        val run = runs.start(tenant.id, sources.create(tenant.id, tenant.draft(source)).id)
        val step = run.steps.single().id
        assertTrue(steps.claim(tenant.id, step))
        clock.now = RUNS_NOW.plusSeconds(5)
        if (started) assertTrue(steps.accepted(tenant.id, step, "accepted"))
        clock.now = RUNS_NOW.plusSeconds(155)
        if (report == null) {
            // The lost deadline has come (FXs): lost() only takes a step whose deadline is due.
            jdbc.update("update run_steps set lost_deadline = ? where id = ?", Timestamp.from(clock.now), step)
            assertTrue(steps.lost(tenant.id, step))
        } else {
            results.record(tenant.id, tenant.agentId, step, report)
        }
        return run.id
    }

    private fun failed(
        message: String?,
        output: StepOutput? = null,
        source: String = "db-main",
    ) = finished(StepReport(StepState.FAILED, message, output), source)

    private fun shown(index: Int = 0): String = fake.shownText(index)

    private fun sent(index: Int = 0): String = fake.sentHtml(index)

    private fun status(run: UUID) =
        jdbc.queryForObject("select status from notification_deliveries where run_id = ?", String::class.java, run)

    private fun runRow(run: UUID) = jdbc.queryForMap("select status, finished_at from runs where id = ?", run)

    private fun ticks(count: Int = 3) = repeat(count) { service.tick() }

    // --- one message per run

    @Test
    fun `Успешный запуск даёт одно сообщение`() {
        val run = finished(SUCCEEDED)

        ticks()

        assertEquals(1, fake.requests.size)
        assertEquals("delivered", status(run))
    }

    @Test
    fun `Неуспешный запуск даёт одно сообщение`() {
        failed("restic exited with code 1")

        ticks()

        assertEquals(1, fake.requests.size)
    }

    @Test
    fun `Поздний результат после потери шага второго сообщения не даёт`() {
        val run = finished(report = null)
        ticks()
        assertEquals(1, fake.requests.size)
        val step = jdbc.queryForObject("select id from run_steps where run_id = ?", UUID::class.java, run)!!

        results.record(tenant.id, tenant.agentId, step, SUCCEEDED)
        ticks()

        assertEquals(1, fake.requests.size)
    }

    @Test
    fun `Два запуска, завершённые одновременно, дают по одному сообщению`() {
        val first = finished(SUCCEEDED, "db-one")
        val second = finished(SUCCEEDED, "db-two")

        ticks()

        assertEquals(2, fake.requests.size)
        val texts = fake.requests.indices.map { shown(it) }
        assertEquals(1, texts.count { "db-one" in it && "$first" in it })
        assertEquals(1, texts.count { "db-two" in it && "$second" in it })
    }

    @Test
    fun `Запуск не вручную не уведомляется`() {
        val run = finished(SUCCEEDED)
        val workflow = UUID.randomUUID()
        jdbc.update(NOT_MANUAL_WORKFLOW, workflow, tenant.id)
        jdbc.update(
            "update runs set trigger = 'schedule', workflow_id = ?, definition = '{}'::jsonb where id = ?",
            workflow,
            run,
        )

        ticks()

        assertEquals(0, fake.requests.size)
        assertEquals("skipped", status(run))
    }

    /** A scheduled run of [sourceId] queued [hour] hours after T0 that ends with [report] 2 min 30 s later. */
    private fun scheduled(
        sourceId: UUID,
        report: StepReport,
        hour: Long,
    ): UUID {
        clock.now = RUNS_NOW.plusSeconds(3600 * hour)
        val run = runs.start(tenant.id, sourceId)
        jdbc.update("update runs set trigger = 'schedule' where id = ?", run.id)
        val step = run.steps.single().id
        assertTrue(steps.claim(tenant.id, step))
        assertTrue(steps.accepted(tenant.id, step, "accepted"))
        clock.now = clock.now.plusSeconds(150)
        results.record(tenant.id, tenant.agentId, step, report)
        ticks()
        return run.id
    }

    @Test
    fun `scheduled runs notify the first failure of a series and the recovery, through the queue`() {
        val source = sources.create(tenant.id, tenant.draft("db-main")).id
        val failure = StepReport(StepState.FAILED, "disk full", null)

        val quiet = scheduled(source, SUCCEEDED, 0)
        val first = scheduled(source, failure, 1)
        val again = scheduled(source, failure, 2)
        val back = scheduled(source, SUCCEEDED, 3)

        val statuses = listOf(quiet, first, again, back).map(::status)
        assertEquals(listOf("skipped", "delivered", "skipped", "delivered"), statuses)
        assertEquals(2, fake.requests.size)
        assertEquals("❌ Бэкап завершился ошибкой: db-main", shown(0).lines().first())
        assertEquals("✅ Бэкап снова работает: db-main", shown(1).lines().first())
    }

    // --- the text, end to end

    @Test
    fun `Полный текст сообщения об успехе на русском в Telegram`() {
        val run = finished(SUCCEEDED)

        ticks(1)

        val expected =
            """
            ✅ Бэкап выполнен: db-main
            Агент: db1
            Длительность: 2 мин 30 с
            Всего: 1,5 ГиБ, добавлено: 12,3 МиБ
            Запуск: $run
            $CONSOLE/runs/$run
            """.trimIndent()
        assertEquals(expected, shown())
        assertEquals(CHAT, JSON.readTree(fake.requests.single().body).path("chat_id").asString())
    }

    @Test
    fun `Неверный результат агента приводится с пометкой invalid result`() {
        finished(StepReport(StepState.SUCCEEDED, null, null))

        ticks(1)

        val lines = shown().lines()
        assertEquals("❌ Бэкап завершился ошибкой: db-main", lines[0])
        assertEquals("Причина: invalid result: a succeeded backup without its output", lines[3])
    }

    @Test
    fun `Причина пишется дословно вместе с переводами строк в Telegram`() {
        val reason = "open /data/a: permission denied\nopen /data/b: permission denied"
        failed(reason)

        ticks(1)

        assertTrue("Причина: $reason\n" in shown(), shown())
        assertTrue("<code>$reason</code>" in sent(), sent())
    }

    @Test
    fun `Разметка в причине и имени источника не становится разметкой Telegram`() {
        failed("<script>x</script>", source = "<b>db</b> & co")

        ticks(1)

        assertTrue("&lt;b&gt;db&lt;/b&gt; &amp; co" in sent(), sent())
        assertTrue("&lt;script&gt;x&lt;/script&gt;" in sent(), sent())
        assertTrue("<b>db</b> & co" in shown() && "<script>x</script>" in shown(), shown())
    }

    @Test
    fun `Отклонённый агентом шаг без начала выполнения не имеет строки длительности в Telegram`() {
        val run = finished(StepReport(StepState.REJECTED, "unknown plugin \"absent\"", null), started = false)

        ticks(1)

        val expected =
            """
            ❌ Бэкап отклонён агентом: db-main
            Агент: db1
            Причина: unknown plugin "absent"
            Запуск: $run
            $CONSOLE/runs/$run
            """.trimIndent()
        assertEquals(expected, shown())
    }

    @Test
    fun `Ошибка с сохранённым снимком сообщает о снимке в Telegram`() {
        failed("11 files could not be read", SNAPSHOT)

        ticks(1)

        val lines = shown().lines()
        assertEquals("Причина: 11 files could not be read", lines[3])
        assertEquals("Снимок создан и пригоден для восстановления, но часть данных в него не попала.", lines[4])
        assertEquals("Всего: 1,5 ГиБ, добавлено: 12,3 МиБ", lines[5])
    }

    @Test
    fun `Полный текст сообщения о потерянном шаге в Telegram`() {
        val run = finished(report = null)

        ticks(1)

        val expected =
            """
            ❌ Бэкап потерян: db-main
            Агент: db1
            Длительность: 2 мин 30 с
            Причина: agent lost the step
            Связь с агентом прервалась, пока шаг выполнялся. Можно запустить бэкап снова.
            Запуск: $run
            $CONSOLE/runs/$run
            """.trimIndent()
        assertEquals(expected, shown())
    }

    @Test
    fun `Потерянный шаг с поздним выводом бэкапа сообщается так же, как без него`() {
        val plain = finished(report = null, source = "db-plain")
        ticks(1)
        val late = finished(report = null, source = "db-late")
        val step = jdbc.queryForObject("select id from run_steps where run_id = ?", UUID::class.java, late)!!
        results.record(tenant.id, tenant.agentId, step, SUCCEEDED)

        ticks(1)

        val withoutOutput = shown(0).replace("db-plain", "db-x").replace("$plain", "RUN")
        val withOutput = shown(1).replace("db-late", "db-x").replace("$late", "RUN")
        assertEquals(withoutOutput, withOutput)
    }

    @Test
    fun `Причина 501 символ обрезается до 500 с меткой и строкой о консоли в Telegram`() {
        failed("x".repeat(501))

        ticks(1)

        val lines = shown().lines()
        assertEquals("Причина: " + "x".repeat(500) + "…", lines[3])
        assertEquals("Полный текст — в консоли.", lines[4])
    }

    @Test
    fun `Сообщение с максимальными полями не обрезается пределом Telegram`() {
        val name = "s".repeat(200)
        jdbc.update("update agents set hostname = ? where id = ?", "h".repeat(253), tenant.agentId)
        val run = failed("m".repeat(10_000), SNAPSHOT, name)

        ticks(1)

        val text = shown()
        assertTrue(text.length < 4096, "${text.length}")
        assertTrue(text.endsWith("$CONSOLE/runs/$run"), text)
        assertTrue(name in text && "h".repeat(253) in text)
    }

    @Test
    fun `Значения конфигурации источника в сообщение не попадают`() {
        val repository = "qa-repo-marker"
        jdbc.update(
            "insert into agent_repositories (tenant_id, agent_id, name, backend) values (?, ?, ?, 'local')",
            tenant.id,
            tenant.agentId,
            repository,
        )
        val config = """{"paths": ["/srv/QA-CONFIG-MARKER"], "exclude": ["*.QA-EXCLUDE-MARKER"]}"""
        val source = sources.create(tenant.id, tenant.draft("db-main", repository = repository, config = config))
        val run = runs.start(tenant.id, source.id)
        val step = run.steps.single().id
        assertTrue(steps.claim(tenant.id, step))
        results.record(tenant.id, tenant.agentId, step, StepReport(StepState.FAILED, "boom", SNAPSHOT))

        ticks(1)

        for (marker in listOf("QA-CONFIG-MARKER", "QA-EXCLUDE-MARKER", repository)) {
            assertFalse(marker in sent(), "$marker is in: ${sent()}")
        }
    }

    @Test
    fun `Токен бота в текст сообщения не попадает`() {
        finished(SUCCEEDED)

        ticks(1)

        assertFalse(TEST_TOKEN in sent())
    }

    @Test
    fun `Повторная отправка после сбоя даёт сообщение с тем же текстом`() {
        finished(SUCCEEDED)
        ticks(1)
        // The Bot API took the message and the answer was lost: the row is as the sender left it before it recorded.
        jdbc.update("update notification_deliveries set status = 'pending', finished_at = null")
        clock.now = clock.now + Duration.ofMinutes(5)

        ticks(1)

        assertEquals(2, fake.requests.size)
        assertEquals(sent(0), sent(1))
    }

    // --- a failure of delivery does not touch the run

    @Test
    fun `Отказ Telegram не меняет запуск и пишется в журнал с причиной`(output: CapturedOutput) {
        val run = finished(SUCCEEDED)
        val before = runRow(run)
        fake.reply(FakeBotApi.error(400, "Bad Request: chat not found"))

        ticks(1)

        assertEquals(before, runRow(run))
        assertEquals("succeeded", before["status"])
        assertTrue("Notification of run $run through telegram failed" in output.all, "the failure is logged")
        assertTrue("chat not found" in output.all, "with its reason")
        assertEquals("failed", status(run))
    }

    @Test
    fun `Недоступный Telegram не меняет запуск, сообщение уходит после восстановления`() {
        val run = finished(StepReport(StepState.FAILED, "boom", null))
        val before = runRow(run)
        fake.fallback = BotReply(503, "{}")
        ticks(1)

        fake.fallback = FakeBotApi.ok()
        clock.now = clock.now + Duration.ofSeconds(10)
        ticks(1)

        assertEquals(2, fake.requests.size)
        assertEquals(before, runRow(run))
        assertEquals("delivered", status(run))
    }

    @Test
    fun `Неотправленное за срок жизни пишется в журнал как expired`(output: CapturedOutput) {
        val run = finished(SUCCEEDED)
        fake.fallback = BotReply(503, "{}")
        ticks(1)

        clock.now = clock.now + Duration.ofHours(24)
        ticks(1)

        assertTrue("Notification of run $run through telegram expired" in output.all)
        assertEquals("expired", status(run))
    }

    @Test
    fun `Сбой базы во время отправки не теряет сообщение`() {
        val run = finished(SUCCEEDED)
        val before = runRow(run)
        jdbc.execute("alter table notification_deliveries rename to notification_deliveries_off")
        try {
            assertFailsWith<Exception> { service.tick() }
        } finally {
            jdbc.execute("alter table notification_deliveries_off rename to notification_deliveries")
        }

        ticks(1)

        assertEquals(1, fake.requests.size)
        assertTrue("$run" in shown())
        assertEquals(before, runRow(run))
    }

    @Test
    fun `Источник, удалённый до отправки, назван в сообщении своим именем`() {
        val run = finished(SUCCEEDED)
        val source = jdbc.queryForObject("select source_id from runs where id = ?", UUID::class.java, run)!!
        sources.delete(tenant.id, source)

        ticks(1)

        assertEquals("✅ Бэкап выполнен: db-main", shown().lines().first())
    }

    // --- start

    @Test
    fun `С ботом нет предупреждения об отсутствии форматтера`(output: CapturedOutput) {
        assertTrue("Notifications go through [telegram]" in output.all)
        assertFalse("no NotificationFormatter bean" in output.all)
    }
}
