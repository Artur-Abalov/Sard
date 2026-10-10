// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.notify

import dev.sard.server.TestcontainersConfiguration
import dev.sard.server.notify.telegram.BotReply
import dev.sard.server.notify.telegram.FakeBotApi
import dev.sard.server.notify.telegram.TEST_TOKEN
import dev.sard.server.persistence.TenantSessions
import dev.sard.server.persistence.UuidV7
import dev.sard.server.pki.MovableClock
import dev.sard.server.runs.RUNS_NOW
import dev.sard.server.runs.RunState
import dev.sard.server.runs.Runs
import dev.sard.server.runs.RunsTenant
import dev.sard.server.runs.RunsTestConfiguration
import dev.sard.server.runs.Sources
import dev.sard.server.runs.StepOutcome
import dev.sard.server.runs.StepOutput
import dev.sard.server.runs.StepReport
import dev.sard.server.runs.StepResults
import dev.sard.server.runs.StepState
import dev.sard.server.runs.StepTransitions
import io.micrometer.core.instrument.MeterRegistry
import org.junit.jupiter.api.AfterAll
import org.junit.jupiter.api.extension.ExtendWith
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.boot.test.context.TestConfiguration
import org.springframework.boot.test.system.CapturedOutput
import org.springframework.boot.test.system.OutputCaptureExtension
import org.springframework.context.annotation.Bean
import org.springframework.context.annotation.Import
import org.springframework.context.annotation.Primary
import org.springframework.jdbc.core.JdbcTemplate
import org.springframework.test.context.DynamicPropertyRegistry
import org.springframework.test.context.DynamicPropertySource
import tools.jackson.databind.json.JsonMapper
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
private const val REPOSITORY_ID = "5f0c3e2d9a8b7c6d5e4f3a2b1c0d9e8f7a6b5c4d3e2f1a0b9c8d7e6f5a4b3c2d"
private val SUCCEEDED = StepReport(StepState.SUCCEEDED, null, StepOutput.Backup("4a3b2c1d", 1_000, 100, REPOSITORY_ID))
private val JSON = JsonMapper.builder().build()
private val LEASE: Duration = Duration.ofMinutes(5)
private const val BY_RUN = "select * from notification_deliveries where run_id = ?"

/** Says what the run is, so a test can tell messages apart; it takes the place of the real formatter. */
@TestConfiguration(proxyBeanMethods = false)
class NotifyTestConfiguration {
    @Bean
    @Primary
    fun formatter() =
        NotificationFormatter { notice ->
            if (notice.sourceName.startsWith("quiet")) {
                null
            } else {
                Message(Message.Bold(notice.status.stored), Message.Text(" "), Message.Code(notice.sourceName))
            }
        }
}

/**
 * S9a, test strategy 1–3: finished runs reach a fake Telegram at least once, through the outbox
 * that the background tick fills from committed runs (OQ-047, answer В4).
 */
@ExtendWith(OutputCaptureExtension::class)
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.NONE,
    properties = [
        "spring.grpc.server.port=0",
        // Everything the notification code could say, so that test 5 sees it.
        "logging.level.dev.sard.server.notify=TRACE",
        "sard.notify.tick-interval=1h",
        "SARD_TELEGRAM_BOT_TOKEN=$TEST_TOKEN",
        "SARD_TELEGRAM_CHAT_ID=$CHAT",
    ],
)
@Import(TestcontainersConfiguration::class, RunsTestConfiguration::class, NotifyTestConfiguration::class)
class NotificationsIntegrationTest(
    @Autowired private val service: NotificationService,
    @Autowired private val loop: NotificationLoop,
    @Autowired private val sources: Sources,
    @Autowired private val runs: Runs,
    @Autowired private val steps: StepTransitions,
    @Autowired private val results: StepResults,
    @Autowired private val sessions: TenantSessions,
    @Autowired private val clock: MovableClock,
    @Autowired private val meters: MeterRegistry,
    @Autowired private val jdbc: JdbcTemplate,
) {
    private val tenant = RunsTenant(jdbc)
    private val other = RunsTenant(jdbc)

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
    fun `two tenants with an agent each`() {
        // The test drives every tick; the loop's own, woken by RunFinished, would race it.
        loop.stop()
        clock.now = RUNS_NOW
        fake.requests.clear()
        fake.fallback = FakeBotApi.ok()
        tenant.create()
        other.create()
    }

    @AfterTest
    fun `drop both tenants`() {
        for (t in listOf(tenant, other)) {
            jdbc.update("delete from notification_deliveries where tenant_id = ?", t.id)
            jdbc.update("delete from snapshots where tenant_id = ?", t.id)
            t.drop()
        }
    }

    /** A run finished by the agent's StepResult through S7a's own transaction. */
    private fun finished(
        t: RunsTenant = tenant,
        name: String = "prod-db",
    ): UUID {
        val run = runs.start(t.id, sources.create(t.id, t.draft(name)).id)
        val step = run.steps.single().id
        assertTrue(steps.claim(t.id, step))
        assertTrue(steps.accepted(t.id, step, "accepted"))
        results.record(t.id, t.agentId, step, SUCCEEDED)
        return run.id
    }

    private fun delivery(run: UUID): Map<String, Any?> = jdbc.queryForMap(BY_RUN, run)

    private fun deliveries(t: RunsTenant): Int? = t.count("notification_deliveries")

    private fun instant(value: Any?): Instant? = (value as Timestamp?)?.toInstant()

    private fun later(duration: Duration) {
        clock.now = clock.now + duration
    }

    // --- 1. Delivery and the Bot API's answers ---

    @Test
    fun `a finished run is sent once to the configured chat`() {
        val run = finished()

        service.tick()
        service.tick()

        val request = fake.requests.single()
        val body = JSON.readTree(request.body)
        assertEquals(CHAT, body.path("chat_id").asString())
        assertEquals("<b>succeeded</b> <code>prod-db</code>", body.path("text").asString())
        val row = delivery(run)
        assertEquals("telegram", row["channel"])
        assertEquals("delivered", row["status"])
        assertEquals(RUNS_NOW, instant(row["finished_at"]))
        assertEquals(0, row["attempts"])
    }

    @Test
    fun `a run the formatter has nothing to say about is skipped, not sent`() {
        val run = finished(name = "quiet-db")

        service.tick()
        service.tick()

        assertEquals(0, fake.requests.size)
        val row = delivery(run)
        assertEquals("skipped", row["status"])
        assertEquals(RUNS_NOW, instant(row["finished_at"]))
    }

    @Test
    fun `runs of every tenant are told about, each in its own tenant`() {
        val mine = finished(tenant, "mine")
        val theirs = finished(other, "theirs")

        service.tick()

        assertEquals(2, fake.requests.size)
        assertEquals(tenant.id, delivery(mine)["tenant_id"])
        assertEquals(other.id, delivery(theirs)["tenant_id"])
    }

    @Test
    fun `a run that has not finished is not told about`() {
        runs.start(tenant.id, sources.create(tenant.id, tenant.draft()).id)

        service.tick()

        assertEquals(0, fake.requests.size)
        assertEquals(0, deliveries(tenant))
    }

    @Test
    fun `429 waits retry_after without counting an attempt`() {
        val run = finished()
        fake.reply(FakeBotApi.tooMany(37))

        service.tick()
        val waiting = delivery(run)
        assertEquals("pending", waiting["status"])
        assertEquals(0, waiting["attempts"])
        assertEquals(RUNS_NOW.plusSeconds(37), instant(waiting["next_attempt_at"]))
        assertEquals("HTTP 429: retry after 37 s", waiting["last_error"])

        later(Duration.ofSeconds(36))
        service.tick()
        assertEquals(1, fake.requests.size)

        later(Duration.ofSeconds(1))
        service.tick()
        assertEquals(2, fake.requests.size)
        assertEquals("delivered", delivery(run)["status"])
    }

    @Test
    fun `5xx is retried with backoff and gives up after the attempt limit`() {
        val run = finished()
        fake.fallback = FakeBotApi.error(500, "Internal Server Error")
        val waits = mutableListOf<Long>()

        repeat(20) {
            service.tick()
            val row = delivery(run)
            if (row["status"] != "pending") return@repeat
            val next = instant(row["next_attempt_at"])!!
            waits += Duration.between(clock.now, next).seconds
            clock.now = next
        }

        assertEquals(8, fake.requests.size)
        assertEquals(listOf(10L, 20, 40, 80, 160, 320, 600), waits)
        val row = delivery(run)
        assertEquals("failed", row["status"])
        assertEquals(8, row["attempts"])
        assertEquals("gave up after 8 attempts: HTTP 500: Internal Server Error", row["last_error"])
    }

    @Test
    fun `4xx other than 429 fails at once and is never retried`() {
        val run = finished()
        fake.reply(FakeBotApi.error(400, "Bad Request: chat not found"))

        service.tick()
        later(Duration.ofHours(1))
        service.tick()

        assertEquals(1, fake.requests.size)
        val row = delivery(run)
        assertEquals("failed", row["status"])
        assertEquals("HTTP 400: Bad Request: chat not found", row["last_error"])
    }

    @Test
    fun `a delivery not made within the time to live expires`() {
        val run = finished()
        fake.fallback = BotReply(503, "{}")
        service.tick()

        later(Duration.ofHours(25))
        service.tick()

        assertEquals(1, fake.requests.size)
        assertEquals("expired", delivery(run)["status"])
    }

    @Test
    fun `runs finished longer ago than the time to live are not planned`() {
        finished()
        later(Duration.ofHours(24).plusSeconds(1))

        service.tick()

        assertEquals(0, deliveries(tenant))
        assertEquals(0, fake.requests.size)
    }

    // --- 5. The bot token stays out of logs, errors and the database ---

    @Test
    fun `the bot token never reaches the logs or the database, on success or failure`(output: CapturedOutput) {
        val failing = finished(name = "failing")
        fake.reply(
            FakeBotApi.error(500, "upstream said bot$TEST_TOKEN"),
            FakeBotApi.tooMany(1),
            FakeBotApi.error(401, "Unauthorized: bot$TEST_TOKEN"),
        )
        service.tick()
        later(Duration.ofSeconds(10))
        service.tick()
        later(Duration.ofSeconds(1))
        service.tick()
        val delivered = finished(name = "fine")
        service.tick()

        assertEquals("failed", delivery(failing)["status"])
        assertEquals("delivered", delivery(delivered)["status"])
        assertTrue("HTTP 401: Unauthorized: bot[REDACTED]" in output.all, "the failure itself is logged")
        val encoded = TEST_TOKEN.replace(":", "%3A")
        for (secret in listOf(TEST_TOKEN, encoded)) {
            assertFalse(secret in output.all, "the token is in the log")
            val stored = jdbc.queryForList("select last_error from notification_deliveries", String::class.java)
            assertFalse(stored.any { it != null && secret in it }, "the token is in last_error: $stored")
        }
    }

    // --- Metrics (answer В11) ---

    @Test
    fun `the queue counts what it sent, retried and gave up on, per channel`() {
        fun count(
            name: String,
            vararg tags: String,
        ) = meters
            .find(name)
            .tags(*tags)
            .counter()
            ?.count() ?: 0.0
        val sent = count("sard.notify.sent", "channel", "telegram")
        val retried = count("sard.notify.retries", "channel", "telegram")
        val failed = count("sard.notify.undelivered", "channel", "telegram", "reason", "failed")
        fake.reply(FakeBotApi.error(502, "Bad Gateway"), FakeBotApi.error(400, "Bad Request"))
        val bad = finished(name = "bad")

        service.tick()
        assertEquals(1.0, meters.get("sard.notify.pending").gauge().value())
        later(Duration.ofSeconds(10))
        service.tick()
        finished(name = "good")
        service.tick()

        assertEquals("failed", delivery(bad)["status"])
        assertEquals(sent + 1, count("sard.notify.sent", "channel", "telegram"))
        assertEquals(retried + 1, count("sard.notify.retries", "channel", "telegram"))
        assertEquals(failed + 1, count("sard.notify.undelivered", "channel", "telegram", "reason", "failed"))
        assertEquals(0.0, meters.get("sard.notify.pending").gauge().value())
    }

    // --- 2. A crash after the run committed ---

    @Test
    fun `a run committed while no sender ran is sent after the restart`() {
        val run = finished()
        val restarted =
            NotificationService(
                Deliveries(sessions, UuidV7(clock, SecureRandom())),
                listOf(CapturingChannel()),
                { Message(Message.Text("after restart")) },
                { Message(Message.Text("alert after restart")) },
                RetryPolicy(RetrySettings()),
                QueueSettings(batch = 10, lease = LEASE, ttl = Duration.ofHours(24)),
                clock,
            )

        restarted.tick()

        assertEquals("delivered", delivery(run)["status"])
    }

    @Test
    fun `a sender that died during the send sends again when the lease ends`() {
        val run = finished()
        val deliveriesOfTest = Deliveries(sessions, UuidV7(clock, SecureRandom()))
        deliveriesOfTest.plan(tenant.id, listOf(run), "telegram", clock.now)
        val due = deliveriesOfTest.due(clock.now, listOf("telegram"), 10).single()
        // The sender claimed the row and crashed before it recorded the outcome.
        deliveriesOfTest.claim(due, clock.now, clock.now + LEASE)

        service.tick()
        assertEquals(0, fake.requests.size)

        later(LEASE)
        service.tick()
        assertEquals(1, fake.requests.size)
        assertEquals("delivered", delivery(run)["status"])
    }

    // --- 3. The run's transaction rolled back ---

    @Test
    fun `a run whose finishing transaction rolled back is not told about`() {
        val run = runs.start(tenant.id, sources.create(tenant.id, tenant.draft()).id)
        val step = run.steps.single().id
        assertTrue(steps.claim(tenant.id, step))
        assertTrue(steps.accepted(tenant.id, step, "accepted"))

        assertFailsWith<IllegalStateException> {
            sessions.inTenant(tenant.id) { session ->
                val outcome = StepOutcome(StepState.SUCCEEDED, null)
                assertTrue(steps.finish(session, tenant.id, step, outcome, output = null))
                error("crash before commit")
            }
        }
        service.tick()

        assertEquals("running", jdbc.queryForObject("select status from runs where id = ?", String::class.java, run.id))
        assertEquals(0, deliveries(tenant))
        assertEquals(0, fake.requests.size)
    }

    @Test
    fun `the notice carries the run, its source and its agent`() {
        val run = finished()
        val due = Deliveries(sessions, UuidV7(clock, SecureRandom()))
        due.plan(tenant.id, listOf(run), "test-channel", clock.now)
        val delivery = due.due(clock.now, listOf("test-channel"), 10).single()

        val notice = (due.claim(delivery, clock.now, clock.now + LEASE) as Claimed.Finished).notice

        assertEquals(run, notice.runId)
        assertEquals(tenant.id, notice.tenantId)
        assertEquals(RunState.SUCCEEDED, notice.status)
        assertEquals("prod-db", notice.sourceName)
        assertEquals(tenant.agentId, notice.agentId)
        assertEquals("db1", notice.agentHostname)
        assertEquals(RUNS_NOW, notice.finishedAt)
        assertEquals(null, due.claim(delivery, clock.now, clock.now + LEASE), "a claimed delivery is not due")
    }

    private fun noticeOf(run: UUID): RunNotice {
        val queue = Deliveries(sessions, UuidV7(clock, SecureRandom()))
        queue.plan(tenant.id, listOf(run), "test-channel", clock.now)
        val due = queue.due(clock.now, listOf("test-channel"), 10).single()
        return (queue.claim(due, clock.now, clock.now + LEASE) as Claimed.Finished).notice
    }

    @Test
    fun `the notice carries the step status, its start and the backup output`() {
        val notice = noticeOf(finished())

        assertEquals(StepState.SUCCEEDED, notice.stepStatus)
        assertEquals(RUNS_NOW, notice.startedAt)
        assertEquals(BackupSizes(totalBytes = 1_000, addedBytes = 100), notice.backup)
    }

    @Test
    fun `the notice of a failed step keeps the backup output the step left`() {
        val run = runs.start(tenant.id, sources.create(tenant.id, tenant.draft()).id)
        val step = run.steps.single().id
        assertTrue(steps.claim(tenant.id, step))
        assertTrue(steps.accepted(tenant.id, step, "accepted"))
        val failed = StepReport(StepState.FAILED, "11 files could not be read", SUCCEEDED.output)
        results.record(tenant.id, tenant.agentId, step, failed)

        val notice = noticeOf(run.id)

        assertEquals(StepState.FAILED, notice.stepStatus)
        assertEquals("11 files could not be read", notice.message)
        assertEquals(BackupSizes(totalBytes = 1_000, addedBytes = 100), notice.backup)
    }

    @Test
    fun `the notice of a step rejected before it started has no start and no output`() {
        val run = runs.start(tenant.id, sources.create(tenant.id, tenant.draft()).id)
        val step = run.steps.single().id
        assertTrue(steps.claim(tenant.id, step))
        results.record(tenant.id, tenant.agentId, step, StepReport(StepState.REJECTED, "unknown plugin", null))

        val notice = noticeOf(run.id)

        assertEquals(StepState.REJECTED, notice.stepStatus)
        assertEquals(null, notice.startedAt)
        assertEquals(null, notice.backup)
    }
}
