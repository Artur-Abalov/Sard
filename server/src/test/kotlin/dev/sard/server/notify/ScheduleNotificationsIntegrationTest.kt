// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.notify

import dev.sard.server.TestcontainersConfiguration
import dev.sard.server.notify.telegram.FakeBotApi
import dev.sard.server.notify.telegram.TEST_TOKEN
import dev.sard.server.persistence.TenantSessions
import dev.sard.server.persistence.UuidV7
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
import dev.sard.server.runs.StepsQueued
import dev.sard.server.scheduler.QUIET_LOOP
import dev.sard.server.scheduler.ScheduleDraft
import dev.sard.server.scheduler.ScheduleTables
import dev.sard.server.scheduler.Scheduler
import dev.sard.server.scheduler.SchedulerMetrics
import dev.sard.server.scheduler.SchedulerSettings
import dev.sard.server.scheduler.Schedules
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
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
import java.security.SecureRandom
import java.sql.Timestamp
import java.time.Duration
import java.time.Instant
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
private val SUCCEEDED =
    StepReport(StepState.SUCCEEDED, null, StepOutput.Backup("4a3b2c1d", 1_610_612_736, 12_897_485, REPOSITORY_ID))
private val FAILED = StepReport(StepState.FAILED, "disk full", null)
private val FIRE: UUID = UUID.fromString("0192f0c4-7a10-7000-8000-00000000c001")
private const val NIGHTLY = "0 2 * * *"
private const val BERLIN = "Europe/Berlin"
private val TTL: Duration = Duration.ofHours(24)

/**
 * F3b: the alert about fires skipped in a row and the messages about scheduled runs, through the real queue. The
 * queue and the reading of what a delivery tells about are mutated; the formatters have their own tests.
 */
@MutFlowTest(includeTargets = [Deliveries::class, Notices::class, NotificationService::class])
@ExtendWith(OutputCaptureExtension::class)
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.NONE,
    properties = [
        "spring.grpc.server.port=0",
        QUIET_LOOP,
        "sard.notify.tick-interval=1h",
        "SARD_TELEGRAM_BOT_TOKEN=$TEST_TOKEN",
        "SARD_TELEGRAM_CHAT_ID=$CHAT",
        "SARD_NOTIFY_LANGUAGE=ru",
        "SARD_CONSOLE_PUBLIC_URL=$CONSOLE",
    ],
)
@Import(TestcontainersConfiguration::class, RunsTestConfiguration::class)
class ScheduleNotificationsIntegrationTest(
    @Autowired private val service: NotificationService,
    @Autowired private val loop: NotificationLoop,
    @Autowired private val scheduler: Scheduler,
    @Autowired private val schedules: Schedules,
    @Autowired private val sources: Sources,
    @Autowired private val runs: Runs,
    @Autowired private val steps: StepTransitions,
    @Autowired private val results: StepResults,
    @Autowired private val clock: MovableClock,
    @Autowired private val jdbc: JdbcTemplate,
    @Autowired private val sessions: TenantSessions,
    @Autowired private val settings: SchedulerSettings,
    @Autowired private val formatter: NotificationFormatter,
    @Autowired private val alertFormatter: SkipAlertFormatter,
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
        jdbc.execute("drop trigger if exists fail_alert_fires on schedule_fires")
        jdbc.update("delete from notification_deliveries where tenant_id = ?", tenant.id)
        jdbc.update("delete from schedule_fires where tenant_id = ?", tenant.id)
        jdbc.update("update runs set schedule_id = null where tenant_id = ?", tenant.id)
        jdbc.update("delete from schedules where tenant_id = ?", tenant.id)
        jdbc.update("delete from snapshots where tenant_id = ?", tenant.id)
        tenant.drop()
    }

    private fun tick(by: NotificationService = service) = MutFlow.underTest { by.tick() }

    private fun ticks(count: Int = 3) = repeat(count) { tick() }

    private fun alerts(): List<String> =
        fake.requests.indices
            .map { fake.shownText(it) }
            .filter { it.startsWith("⚠️") }

    private fun scheduleOf(
        name: String = "db-main",
        notifyOnSuccess: Boolean = false,
        draft: dev.sard.server.runs.SourceDraft = tenant.draft(name),
    ): Pair<UUID, UUID> {
        val source = sources.create(tenant.id, draft)
        val schedule = schedules.set(tenant.id, source.id, ScheduleDraft(NIGHTLY, BERLIN, true, notifyOnSuccess))
        return source.id to schedule.id
    }

    /** The fire of the night [day] of October 2026 (02:00 in Berlin is 00:00 UTC); the tick comes 30 s after it. */
    private fun fireNight(
        day: Int,
        by: Scheduler = scheduler,
    ) {
        clock.now = Instant.parse("2026-10-%02dT00:00:30Z".format(day))
        by.tick()
    }

    private fun alertRow(
        schedule: UUID,
        outcome: String = "refused",
        reason: String? = "unknown_plugin",
        id: UUID = FIRE,
    ) = jdbc.update(
        """
        insert into schedule_fires (id, tenant_id, schedule_id, kind, scheduled_for, outcome, reason, skipped_in_row,
                                    alert, recorded_at)
        values (?, ?, ?, 'schedule', ?, ?, ?, 3, true, ?)
        """.trimIndent(),
        id,
        tenant.id,
        schedule,
        Timestamp.from(Instant.parse("2026-10-01T00:00:00Z")),
        outcome,
        reason,
        Timestamp.from(clock.now),
    )

    // --- the alert: full text and one per series

    @Test
    fun `Три пропуска подряд из-за идущего запуска дают один алерт, дальнейшие пропуски нового не дают`() {
        val (source, _) = scheduleOf()
        val active = runs.start(tenant.id, source)

        fireNight(1)
        fireNight(2)
        ticks()
        assertEquals(emptyList(), alerts(), "two skips in a row do not alert")

        fireNight(3)
        ticks()
        val expected =
            """
            ⚠️ Бэкап по расписанию не выполняется: db-main
            Агент: db1
            Пропущено подряд: 3
            Причина: предыдущий запуск всё ещё идёт
            Пропущенное срабатывание: 2026-10-03 02:00 Europe/Berlin
            Идущий запуск: ${active.id}
            $CONSOLE/runs/${active.id}
            """.trimIndent()
        assertEquals(listOf(expected), alerts())

        fireNight(4)
        fireNight(5)
        ticks()
        assertEquals(1, alerts().size, "the fourth and the fifth skip are the same series")
    }

    @Test
    fun `После созданного запуска новая серия из трёх пропусков даёт второй алерт`() {
        val (source, _) = scheduleOf()
        runs.start(tenant.id, source)
        for (night in 1..3) fireNight(night)
        ticks()
        ScheduleTables(jdbc, tenant).finishAll(clock.now)

        for (night in 4..7) fireNight(night)
        ticks()

        assertEquals(2, alerts().size, "night 4 created a run, 5 to 7 are three skips")
        assertTrue(alerts().all { "Пропущено подряд: 3" in it })
        fireNight(8)
        ticks()
        assertEquals(2, alerts().size)
    }

    @Test
    fun `Простой сервера пропуском подряд не считается и алерта не даёт`() {
        scheduleOf()

        clock.now = Instant.parse("2026-10-05T12:00:00Z")
        scheduler.tick()
        ticks()

        assertEquals(emptyList(), alerts())
        assertEquals(0, jdbc.queryForObject("select count(*) from notification_deliveries", Int::class.java))
    }

    @Test
    fun `Алерт и сообщение о запуске - разные сообщения`() {
        val (source, _) = scheduleOf()
        val active = runs.start(tenant.id, source)
        for (night in 1..3) fireNight(night)
        val step = active.steps.single().id
        assertTrue(steps.claim(tenant.id, step))
        assertTrue(steps.accepted(tenant.id, step, "accepted"))
        results.record(tenant.id, tenant.agentId, step, FAILED)

        ticks()

        val texts = fake.requests.indices.map { fake.shownText(it) }
        assertEquals(2, texts.size)
        assertEquals(1, texts.count { it.startsWith("⚠️ Бэкап по расписанию не выполняется") })
        assertEquals(1, texts.count { it.startsWith("❌ Бэкап завершился ошибкой") })
    }

    @Test
    fun `Источник, удалённый до отправки алерта, назван своим именем`() {
        val (source, schedule) = scheduleOf()
        alertRow(schedule)
        jdbc.update("update sources set deleted_at = ? where id = ?", Timestamp.from(clock.now), source)

        ticks()

        assertEquals("⚠️ Бэкап по расписанию не выполняется: db-main", alerts().single().lines().first())
    }

    @Test
    fun `Значения конфигурации источника в алерт не попадают`() {
        val agent = UUID.randomUUID()
        tenant.insertAgent(agent, repositories = listOf("qa-repo-marker"))
        val config = """{"paths":["/srv/QA-CONFIG-MARKER"]}"""
        val draft = tenant.draft("db-main", agent, repository = "qa-repo-marker", config = config)
        val (_, schedule) = scheduleOf(draft = draft)
        alertRow(schedule, reason = "unknown_repository")

        ticks()

        val text = alerts().single()
        assertTrue("Причина: агент больше не сообщает репозиторий источника" in text)
        assertFalse("QA-CONFIG-MARKER" in text || "qa-repo-marker" in text, text)
    }

    @Test
    fun `При смешанных причинах алерт называет причину третьего пропуска`() {
        val (source, _) = scheduleOf()
        runs.start(tenant.id, source)
        fireNight(1)
        fireNight(2)
        ScheduleTables(jdbc, tenant).finishAll(clock.now)
        jdbc.update("update agents set revoked_at = ? where id = ?", Timestamp.from(clock.now), tenant.agentId)

        fireNight(3)
        ticks()

        val alert = alerts().single()
        assertTrue("Причина: агент источника отозван" in alert.lines(), alert)
        assertFalse("предыдущий запуск всё ещё идёт" in alert, alert)
    }

    @Test
    fun `Порог алерта берётся из настройки`() {
        val (source, _) = scheduleOf()
        runs.start(tenant.id, source)
        val strict =
            Scheduler(
                sessions,
                clock,
                runs,
                UuidV7(clock, SecureRandom()),
                StepsQueued.NONE,
                settings.copy(skipAlertThreshold = 5),
                SchedulerMetrics.NONE,
            )

        for (night in 1..4) fireNight(night, strict)
        ticks()
        assertEquals(emptyList(), alerts(), "the third and the fourth skip are below the threshold")

        fireNight(5, strict)
        ticks()
        assertEquals(listOf("Пропущено подряд: 5"), alerts().single().lines().filter { "Пропущено подряд" in it })
    }

    // --- delivery as reliable as a run's

    @Test
    fun `Алерт, записанный перед остановкой сервера, доставляется после старта`() {
        val (_, schedule) = scheduleOf()
        alertRow(schedule)
        val restarted =
            NotificationService(
                Deliveries(sessions, UuidV7(clock, SecureRandom())),
                listOf(CapturingChannel()),
                formatter,
                alertFormatter,
                RetryPolicy(RetrySettings()),
                QueueSettings(batch = 10, lease = Duration.ofMinutes(5), ttl = TTL),
                clock,
            )

        repeat(3) { tick(restarted) }

        assertEquals("delivered", deliveryStatus())
    }

    @Test
    fun `Алерт, отправка которого оборвалась, не уходит снова до конца аренды`(output: CapturedOutput) {
        val (_, schedule) = scheduleOf()
        alertRow(schedule)
        val channel =
            object : NotificationChannel {
                var calls = 0
                override val name = "telegram"

                override fun send(message: Message): SendOutcome {
                    calls++
                    check(calls > 1) { "connection lost" }
                    return SendOutcome.Delivered
                }
            }
        val lease = Duration.ofMinutes(5)
        val queue =
            NotificationService(
                Deliveries(sessions, UuidV7(clock, SecureRandom())),
                listOf(channel),
                formatter,
                alertFormatter,
                RetryPolicy(RetrySettings()),
                QueueSettings(batch = 10, lease = lease, ttl = TTL),
                clock,
            )

        tick(queue)
        assertEquals(1, channel.calls)
        assertEquals("pending", deliveryStatus())
        assertTrue("of schedule fire $FIRE failed; it is tried again later" in output.all)

        clock.now = clock.now.plus(lease).minusSeconds(1)
        tick(queue)
        assertEquals(1, channel.calls, "the lease has not ended: nobody else sends it")

        clock.now = clock.now.plusSeconds(2)
        tick(queue)
        assertEquals(2, channel.calls)
        assertEquals("delivered", deliveryStatus())
    }

    @Test
    fun `Срабатывание, откатившееся вместе с журналом, алерта не даёт`() {
        val (source, schedule) = scheduleOf()
        runs.start(tenant.id, source)
        fireNight(1)
        fireNight(2)
        jdbc.execute(
            """
            create or replace function fail_alert_fire() returns trigger language plpgsql as
            ${'$'}${'$'} begin if new.alert then raise exception 'server stopped'; end if; return new; end ${'$'}${'$'}
            """.trimIndent(),
        )
        jdbc.execute(
            "create trigger fail_alert_fires before insert on schedule_fires " +
                "for each row execute function fail_alert_fire()",
        )

        fireNight(3)
        ticks()

        assertEquals(emptyList(), alerts())
        val count = "select count(*) from schedule_fires where schedule_id = ?"
        assertEquals(2, jdbc.queryForObject(count, Int::class.java, schedule), "the third skip left no row")
        assertEquals(0, jdbc.queryForObject("select count(*) from notification_deliveries", Int::class.java))
    }

    @Test
    fun `Сбой базы во время отправки алерта не теряет его`() {
        val (_, schedule) = scheduleOf()
        alertRow(schedule)
        jdbc.execute("alter table notification_deliveries rename to notification_deliveries_off")
        try {
            assertFailsWith<Exception> { tick() }
        } finally {
            jdbc.execute("alter table notification_deliveries_off rename to notification_deliveries")
        }

        ticks(1)

        assertEquals(1, alerts().size)
        assertEquals(1, fake.requests.size)
    }

    @Test
    fun `Недоступный Telegram откладывает алерт до восстановления, журнал срабатываний не меняется`() {
        val (_, schedule) = scheduleOf()
        alertRow(schedule)
        fake.fallback = FakeBotApi.error(503, "Service Unavailable")
        tick()
        assertEquals("pending", deliveryStatus())

        fake.fallback = FakeBotApi.ok()
        clock.now = clock.now.plusSeconds(10)
        tick()

        assertEquals("delivered", deliveryStatus())
        assertEquals(2, fake.requests.size, "the refused attempt and the delivery")
        assertTrue(fake.shownText(1).startsWith("⚠️"))
        val row = jdbc.queryForMap("select skipped_in_row, alert from schedule_fires where id = ?", FIRE)
        assertEquals(listOf(3, true), listOf(row["skipped_in_row"], row["alert"]))
    }

    @Test
    fun `Неотправленный за срок жизни алерт пишется в журнал как expired`(output: CapturedOutput) {
        val (_, schedule) = scheduleOf()
        alertRow(schedule)
        fake.fallback = FakeBotApi.error(503, "Service Unavailable")
        tick()

        clock.now = clock.now.plus(TTL).plusSeconds(1)
        tick()

        assertEquals("expired", deliveryStatus())
        assertTrue("Notification of schedule fire $FIRE through telegram expired" in output.all)
    }

    @Test
    fun `Отказ Telegram по алерту пишется в журнал с причиной`(output: CapturedOutput) {
        val (_, schedule) = scheduleOf()
        alertRow(schedule)
        fake.fallback = FakeBotApi.error(400, "Bad Request: chat not found")

        tick()

        assertEquals("failed", deliveryStatus())
        val line = output.all.lines().single { "Notification of schedule fire $FIRE through telegram failed" in it }
        assertTrue("chat not found" in line, line)
    }

    private fun deliveryStatus() =
        jdbc.queryForObject("select status from notification_deliveries where fire_id = ?", String::class.java, FIRE)

    // --- messages about scheduled runs

    private fun scheduledRun(
        source: UUID,
        schedule: UUID,
        report: StepReport,
        hour: Long,
    ): UUID {
        clock.now = RUNS_NOW.plusSeconds(3600 * hour)
        val run = runs.start(tenant.id, source)
        jdbc.update("update runs set trigger = 'schedule', schedule_id = ? where id = ?", schedule, run.id)
        val step = run.steps.single().id
        assertTrue(steps.claim(tenant.id, step))
        assertTrue(steps.accepted(tenant.id, step, "accepted"))
        clock.now = clock.now.plusSeconds(150)
        results.record(tenant.id, tenant.agentId, step, report)
        return run.id
    }

    @Test
    fun `Настройка уведомления об успехе читается в момент отправки`() {
        val (source, schedule) = scheduleOf()
        scheduledRun(source, schedule, SUCCEEDED, 0)
        schedules.set(tenant.id, source, ScheduleDraft(NIGHTLY, BERLIN, true, notifyOnSuccess = true))

        ticks()

        val lines = fake.shownText().lines()
        assertEquals(listOf("✅ Бэкап выполнен: db-main", "По расписанию"), lines.take(2))
        assertEquals(1, fake.requests.size)
    }

    @Test
    fun `Успех по расписанию без включённого уведомления молчит`() {
        val (source, schedule) = scheduleOf()
        val run = scheduledRun(source, schedule, SUCCEEDED, 0)

        ticks()

        assertEquals(0, fake.requests.size)
        val sql = "select status from notification_deliveries where run_id = ?"
        val status = jdbc.queryForObject(sql, String::class.java, run)
        assertEquals("skipped", status)
    }

    @Test
    fun `Восстановление после ошибок подряд любого триггера называет их число`() {
        val (source, schedule) = scheduleOf()
        scheduledRun(source, schedule, SUCCEEDED, 0)
        scheduledRun(source, schedule, FAILED, 1)
        scheduledRun(source, schedule, FAILED, 2)
        scheduledRun(source, schedule, FAILED, 3)
        scheduledRun(source, schedule, SUCCEEDED, 4)

        ticks()

        val texts = fake.requests.indices.map { fake.shownText(it) }
        assertEquals(2, texts.size)
        val recovery = texts.single { it.startsWith("✅") }
        assertTrue("Ошибок подряд перед этим: 3" in recovery.lines(), texts.toString())
    }
}
