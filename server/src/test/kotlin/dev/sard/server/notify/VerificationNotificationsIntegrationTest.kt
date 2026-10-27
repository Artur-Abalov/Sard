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
import dev.sard.server.runs.Runs
import dev.sard.server.runs.RunsTenant
import dev.sard.server.runs.RunsTestConfiguration
import dev.sard.server.runs.Sources
import dev.sard.server.verify.NoVerifierEpisodes
import dev.sard.server.verify.NoVerifierState
import dev.sard.server.verify.VerificationLoop
import dev.sard.server.verify.VerificationTestConfiguration
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import org.junit.jupiter.api.AfterAll
import org.junit.jupiter.api.Tag
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
import kotlin.test.assertTrue

private const val CHAT = "-100778"
private const val CONSOLE = "https://sard.example.com"

/**
 * V6 (verification-notifications.feature): a failed restore verification, a source not verified for too long, a pass
 * after either and the «no verifier» state reach Telegram through the same queue as runs; successes stay silent.
 */
@ExtendWith(OutputCaptureExtension::class)
@Tag("mutflow-isolated")
@MutFlowTest(
    includeTargets =
        [
            VerificationDeliveries::class,
            VerificationNoticeFormatter::class,
            VerificationTelling::class,
            NoVerifierState::class,
        ],
)
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.NONE,
    properties = [
        "spring.grpc.server.port=0",
        "sard.notify.tick-interval=1h",
        "sard.verification.interval=1h",
        "sard.scheduler.interval=1h",
        "sard.freshness.interval=1h",
        "sard.verification.default-timezone=Europe/Berlin",
        "SARD_TELEGRAM_BOT_TOKEN=$TEST_TOKEN",
        "SARD_TELEGRAM_CHAT_ID=$CHAT",
        "SARD_NOTIFY_LANGUAGE=ru",
        "SARD_CONSOLE_PUBLIC_URL=$CONSOLE",
    ],
)
@Import(TestcontainersConfiguration::class, RunsTestConfiguration::class, VerificationTestConfiguration::class)
class VerificationNotificationsIntegrationTest(
    @Autowired private val service: NotificationService,
    @Autowired private val loop: NotificationLoop,
    @Autowired private val verification: VerificationLoop,
    @Autowired private val sources: Sources,
    @Autowired private val runs: Runs,
    @Autowired private val sessions: TenantSessions,
    @Autowired private val clock: MovableClock,
    @Autowired private val jdbc: JdbcTemplate,
) {
    private val seed = VerificationSeeds(jdbc, sources, runs, clock)
    private val tenant = seed.tenant

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
    fun `a tenant with an agent and a verifier`() {
        loop.stop()
        clock.now = RUNS_NOW.plus(Duration.ofHours(2))
        fake.requests.clear()
        fake.fallback = FakeBotApi.ok()
        tenant.create()
        seed.addVerifier()
    }

    @AfterTest
    fun `drop the tenant`() {
        val tables =
            listOf(
                "notification_deliveries",
                "no_verifier_episodes",
                "verification_stale_episodes",
                "restore_verifications",
                "verification_jobs",
                "snapshots",
            )
        for (table in tables) jdbc.update("delete from $table where tenant_id = ?", tenant.id)
        tenant.drop()
    }

    // --- fixtures

    /** One tick of the notification queue, and the verification loop's. */
    private fun ticks(count: Int = 3) = repeat(count) { MutFlow.underTest { service.tick() } }

    private fun texts(): List<String> = List(fake.requests.size) { fake.shownText(it) }

    // --- a failure

    @Test
    fun `the first failure of a source is told once, with the mismatch in words`() {
        val source = seed.watched()
        val record = seed.failedAt(source, 10)

        ticks()

        val expected =
            """
            ❌ Проверка восстановления провалилась: db-main
            Стадия: сравнение отпечатков
            Снимок: 4f3a9c1e
            Верификатор: vrf1.example.com
            Расхождения:
            — таблица public.orders: в источнике 1 204 311 строк, в копии 1 204 310
            Проверка: $record
            $CONSOLE/verifications/$record
            """.trimIndent()
        assertEquals(listOf(expected), texts())
        assertEquals("delivered", seed.statusOf(record))
    }

    @Test
    fun `a second failure in a row is not told, nor is one after a verification that did not take place`() {
        val source = seed.watched()
        seed.record(source, "passed", 1)
        val first = seed.failedAt(source, 10)
        ticks()
        val second = seed.failedAt(source, 20)
        seed.record(source, "cannot_verify", 25)
        val third = seed.failedAt(source, 30)

        ticks()

        assertEquals(1, fake.requests.size)
        assertEquals(listOf("delivered", "skipped", "skipped"), listOf(first, second, third).map(seed::statusOf))
    }

    @Test
    fun `a failure after a pass is told again, and the pass between is told as a recovery`() {
        val source = seed.watched()
        seed.failedAt(source, 10)
        ticks()
        seed.record(source, "passed", 20, Evidence(restoreMillis = 252_000))
        ticks()
        seed.failedAt(source, 30)
        ticks()

        val first = texts().map { it.lines().first() }

        assertEquals(
            listOf(
                "❌ Проверка восстановления провалилась: db-main",
                "✅ Проверка восстановления снова прошла: db-main",
                "❌ Проверка восстановления провалилась: db-main",
            ),
            first,
        )
    }

    @Test
    fun `the stage of a failure is the first failed one, else the comparison when it found mismatches, else none`() {
        val mismatch = """[{"name":"rows:public.orders","expected":"2","actual":"1"}]"""

        fun stages(vararg states: Pair<String, String>) =
            states.joinToString(",", "[", "]") {
                """{"level":1,"name":"${it.first}","state":"${it.second}","exitCode":0,"output":"","imageDigest":""}"""
            }
        val cases =
            mapOf(
                "db-failed" to Evidence(3, stages = stages("restic_check" to "passed", "restore" to "failed")),
                "db-compared" to Evidence(3, mismatches = mismatch, stages = stages("restic_check" to "passed")),
                "db-unknown" to Evidence(3, stages = stages("restic_check" to "passed")),
            )
        for ((name, evidence) in cases) seed.record(seed.watched(name), "failed", 10, evidence)

        ticks()

        val byName = texts().associateBy { it.lines().first().substringAfterLast(": ") }
        assertEquals("Стадия: извлечение", byName.getValue("db-failed").lines()[1])
        assertEquals("Стадия: сравнение отпечатков", byName.getValue("db-compared").lines()[1])
        assertTrue(byName.getValue("db-unknown").lines().none { it.startsWith("Стадия") }, byName.toString())
    }

    @Test
    fun `five mismatches are all listed, a sixth is told as one more`() {
        fun mismatches(count: Int) =
            List(count) { """{"name":"rows:public.t$it","expected":"2","actual":"1"}""" }.joinToString(",", "[", "]")
        seed.record(seed.watched("db-five"), "failed", 10, Evidence(3, mismatches = mismatches(5)))
        seed.record(seed.watched("db-six"), "failed", 10, Evidence(3, mismatches = mismatches(6)))

        ticks()

        val byName = texts().associateBy { it.lines().first().substringAfterLast(": ") }
        val five = byName.getValue("db-five").lines()
        assertEquals(5, five.count { it.startsWith("— ") })
        assertTrue(five.none { it.contains("ещё") }, five.toString())
        val six = byName.getValue("db-six").lines()
        assertEquals(5, six.count { it.startsWith("— таблица") })
        assertTrue("— и ещё 1" in six, six.toString())
    }

    @Test
    fun `a recovery names the time to restore at level 5 only`() {
        val timed = seed.watched("db-timed")
        val shallow = seed.watched("db-shallow")
        for (source in listOf(timed, shallow)) seed.failedAt(source, 10)
        ticks()
        seed.record(timed, "passed", 20, Evidence(level = 5, restoreMillis = 252_000))
        seed.record(shallow, "passed", 20, Evidence(level = 4, restoreMillis = 252_000))

        ticks()

        val byName = texts().filter { it.startsWith("✅") }.associateBy { it.lines().first().substringAfterLast(": ") }
        assertTrue("Время восстановления: 4 мин 12 с" in byName.getValue("db-timed").lines(), byName.toString())
        assertTrue("Время восстановления" !in byName.getValue("db-shallow"), byName.toString())
    }

    @Test
    fun `a verification that did not take place is not told by itself`() {
        for (status in listOf("cannot_verify", "infrastructure_failure", "no_verifier")) {
            val source = seed.watched("db-$status")
            seed.record(source, "passed", 1)
            val record = seed.record(source, status, 10)

            ticks()

            assertEquals("skipped", seed.statusOf(record), status)
        }
        assertEquals(0, fake.requests.size)
    }

    @Test
    fun `a pass without a failure or an episode before it is silent`() {
        val histories =
            listOf(
                emptyList(),
                listOf("passed"),
                listOf("cannot_verify"),
                listOf("failed", "passed"),
            )
        for ((index, history) in histories.withIndex()) {
            val source = seed.watched("db-$index")
            history.forEachIndexed { step, status -> seed.record(source, status, 1L + step) }
            fake.requests.clear()
            ticks()
            val pass = seed.record(source, "passed", 100L + index)

            ticks()

            assertEquals("skipped", seed.statusOf(pass), "history $history")
        }
    }

    @Test
    fun `a manual verification is told by the same rules`() {
        val source = seed.watched()
        seed.record(source, "passed", 1)
        seed.record(source, "failed", 10, Evidence(level = 3, jobReason = "manual"))

        ticks()

        assertEquals(1, texts().count { it.startsWith("❌ Проверка восстановления провалилась") })
    }

    @Test
    fun `a verification run does not give the message of a run`() {
        val source = seed.watched()
        val run = runs.start(tenant.id, source.id).id
        jdbc.update("update runs set trigger = 'verification' where id = ?", run)
        tenant.finish(run)

        ticks()

        assertEquals(0, fake.requests.size)
    }

    @Test
    fun `a source deleted before the send is still named`() {
        val source = seed.watched()
        seed.failedAt(source, 10)
        jdbc.update("update sources set deleted_at = ? where id = ?", Timestamp.from(clock.now), source.id)

        ticks()

        assertEquals("❌ Проверка восстановления провалилась: db-main", texts().single().lines().first())
    }

    @Test
    fun `the output of a stage, the config and the repository never reach the message`() {
        val source = seed.watched()
        seed.failedAt(source, 10)

        ticks()

        val message = texts().single()
        for (marker in listOf("QA-OUTPUT-MARKER", "pg-prod", "password", "secret")) {
            assertTrue(marker !in message, "$marker in $message")
        }
    }

    // --- a source not verified for too long

    @Test
    fun `a stale episode is told once, in the zone of the cycle, with the last outcome`() {
        val source = seed.watched()
        seed.record(source, "passed", 1)
        seed.record(source, "no_verifier", 30)
        val episode = seed.episode(source, openedMinutes = 60)

        ticks()

        val expected =
            """
            ⚠️ Восстановление не проверено дольше 2 дн.: db-main
            Последняя успешная проверка: 2026-09-30 12:01 Europe/Berlin
            Последний итог: нет подходящего верификатора
            $CONSOLE/sources/${source.id}
            """.trimIndent()
        assertEquals(listOf(expected), texts())
        assertEquals(
            "delivered",
            jdbc.queryForObject(
                "select status from notification_deliveries where stale_episode_id = ?",
                String::class.java,
                episode,
            ),
        )
    }

    @Test
    fun `an episode that stays open is told once however many cycles pass`() {
        val source = seed.watched()
        seed.episode(source, openedMinutes = 60)
        ticks()

        repeat(5) {
            verification.tick()
            ticks()
        }

        assertEquals(1, fake.requests.size)
    }

    @Test
    fun `a new episode after a pass is told again`() {
        val source = seed.watched()
        val first = seed.episode(source, openedMinutes = 60)
        ticks()
        val pass = seed.record(source, "passed", 100)
        jdbc.update(
            "update verification_stale_episodes set closed_at = ?, closed_by_id = ? where id = ?",
            Timestamp.from(clock.now),
            pass,
            first,
        )
        ticks()
        seed.episode(source, openedMinutes = 1000, countedFrom = seed.at(100))
        ticks()

        val titles = texts().map { it.lines().first() }

        assertEquals(
            listOf(
                "⚠️ Восстановление не проверено дольше 2 дн.: db-main",
                "✅ Проверка восстановления снова прошла: db-main",
                "⚠️ Восстановление не проверено дольше 2 дн.: db-main",
            ),
            titles,
        )
        assertEquals("delivered", seed.statusOf(pass))
    }

    @Test
    fun `an episode closed by turning verification off gives no message`() {
        val source = seed.watched()
        seed.episode(source, openedMinutes = 60)
        ticks()
        jdbc.update("update sources set verification_enabled = false where id = ?", source.id)

        verification.tick()
        ticks()

        assertEquals(1, fake.requests.size)
    }

    @Test
    fun `a stale episode is told before a failure made in the same tick`() {
        val source = seed.watched()
        seed.failedAt(source, 10)
        seed.episode(source, openedMinutes = 60)

        ticks()

        assertEquals(2, fake.requests.size)
        assertTrue(texts().first().startsWith("⚠️ Восстановление не проверено"), texts().toString())
    }

    @Test
    fun `a series of failures does not hold back the stale episode`() {
        val source = seed.watched()
        seed.record(source, "passed", 1)
        seed.failedAt(source, 10)
        seed.failedAt(source, 20)
        seed.failedAt(source, 30)
        ticks()
        seed.episode(source, openedMinutes = 60)

        ticks()

        assertEquals(1, texts().count { it.startsWith("⚠️ Восстановление не проверено") })
    }

    @Test
    fun `each source that goes stale is told on its own`() {
        val names = listOf("a", "b", "c")
        for (name in names) seed.episode(seed.watched(name), openedMinutes = 60)

        ticks()

        assertEquals(names, texts().map { it.lines().first().substringAfterLast(": ") }.sorted())
    }

    // --- a pass after silence

    @Test
    fun `a pass that closed a stale episode is told as a recovery`() {
        val source = seed.watched()
        seed.record(source, "no_verifier", 10)
        seed.record(source, "no_verifier", 20)
        val open = seed.episode(source, openedMinutes = 30)
        ticks()
        val pass = seed.record(source, "passed", 100, Evidence(level = 4))
        jdbc.update(
            "update verification_stale_episodes set closed_at = ?, closed_by_id = ? where id = ?",
            Timestamp.from(clock.now),
            pass,
            open,
        )

        ticks()

        assertEquals(2, fake.requests.size)
        assertEquals("✅ Проверка восстановления снова прошла: db-main", texts().last().lines().first())
        assertEquals("Уровень: 4 — данные совпали", texts().last().lines()[1])
    }

    @Test
    fun `a pass after failures that also closed an episode is one message`() {
        val source = seed.watched()
        seed.failedAt(source, 10)
        val open = seed.episode(source, openedMinutes = 30)
        ticks()
        val before = fake.requests.size
        val pass = seed.record(source, "passed", 100)
        jdbc.update(
            "update verification_stale_episodes set closed_at = ?, closed_by_id = ? where id = ?",
            Timestamp.from(clock.now),
            pass,
            open,
        )

        ticks()

        assertEquals(1, fake.requests.size - before)
        assertEquals(1, texts().count { "$pass" in it })
    }

    // --- no verifier

    private fun noVerifierSources(count: Int) = repeat(count) { seed.watched("db-$it") }

    private fun revokeVerifier() {
        val now = Timestamp.from(clock.now)
        jdbc.update("update agents set revoked_at = ? where id = ?", now, seed.verifier)
    }

    @Test
    fun `the state of no verifier is told once with the number of sources, not every cycle`() {
        noVerifierSources(3)
        revokeVerifier()
        verification.tick()

        ticks()
        repeat(5) {
            verification.tick()
            ticks()
        }

        val expected =
            """
            ⚠️ Проверка восстановления не работает: нет верификатора
            Источников с включённой проверкой: 3
            Выпустите токен с ролью «верификатор» и поставьте агента: инструкция на обзоре консоли
            $CONSOLE/
            """.trimIndent()
        assertEquals(listOf(expected), texts())
    }

    @Test
    fun `leaving the state is silent, entering it again is told again`() {
        noVerifierSources(1)
        revokeVerifier()
        verification.tick()
        ticks()
        val openEpisodes = {
            jdbc.queryForObject(
                "select count(*) from no_verifier_episodes where tenant_id = ? and closed_at is null",
                Int::class.java,
                tenant.id,
            )
        }
        assertEquals(1, openEpisodes())

        jdbc.update("update agents set revoked_at = null where id = ?", seed.verifier)
        verification.tick()
        ticks()
        assertEquals(0, openEpisodes())
        assertEquals(1, fake.requests.size)

        revokeVerifier()
        verification.tick()
        ticks()

        assertEquals(2, fake.requests.size)
    }

    @Test
    fun `an installation without sources to verify is not in the state`() {
        revokeVerifier()

        verification.tick()
        ticks()

        assertEquals(0, fake.requests.size)
        assertEquals(0, tenant.count("no_verifier_episodes"))
    }

    @Test
    fun `a verifier that fitted no source does not make the state`() {
        val source = seed.watched()
        seed.record(source, "no_verifier", 10)

        verification.tick()
        ticks()

        assertEquals(0, tenant.count("no_verifier_episodes"))
        assertEquals(0, fake.requests.size)
    }

    // --- delivery as reliable as runs'

    @Test
    fun `a failure is held back while Telegram is unavailable and sent when it is back`() {
        val source = seed.watched()
        seed.failedAt(source, 10)
        fake.fallback = BotReply(503, "{}")
        ticks(1)

        fake.fallback = FakeBotApi.ok()
        clock.now = clock.now.plus(Duration.ofMinutes(10))
        ticks(1)

        assertEquals(2, fake.requests.size)
        assertEquals("❌ Проверка восстановления провалилась: db-main", texts().last().lines().first())
    }

    @Test
    fun `a message not sent within a day is logged as expired`(output: CapturedOutput) {
        val source = seed.watched()
        val record = seed.failedAt(source, 10)
        fake.fallback = BotReply(503, "{}")
        ticks(1)

        clock.now = clock.now.plus(Duration.ofHours(25))
        ticks(1)

        assertEquals("expired", seed.statusOf(record))
        assertTrue(
            output.all.lines().any {
                " WARN " in it && "Notification of verification $record through telegram expired" in it
            },
        )
    }

    @Test
    fun `a refusal of Telegram to tell of a stale episode is logged with its reason`(output: CapturedOutput) {
        val source = seed.watched()
        val episode = seed.episode(source, openedMinutes = 60)
        fake.fallback = FakeBotApi.error(400, "Bad Request: chat not found")

        ticks(1)

        val line =
            output.all.lines().single {
                "Notification of stale episode $episode" in it && "through telegram failed" in it
            }
        assertTrue(" WARN " in line && "chat not found" in line, line)
    }

    @Test
    fun `a failure recorded before the server stopped is sent after it starts`() {
        val source = seed.watched()
        seed.failedAt(source, 10)
        val channel = CapturingChannel()
        val restarted = anotherServer(channel)

        repeat(3) { restarted.tick() }

        assertEquals(1, channel.sent.size)
    }

    @Test
    fun `two servers send a failure once`() {
        val source = seed.watched()
        seed.failedAt(source, 10)
        val channel = CapturingChannel()
        val other = anotherServer(channel)

        repeat(3) {
            service.tick()
            other.tick()
        }

        assertEquals(1, fake.requests.size + channel.sent.size)
    }

    private fun anotherServer(channel: CapturingChannel): NotificationService {
        val ids = { UuidV7(clock, SecureRandom()) }
        return NotificationService(
            Deliveries(sessions, ids(), { false }),
            OverdueDeliveries(sessions, ids()),
            CertificateDeliveries(sessions, ids()),
            VerificationDeliveries(sessions, ids()),
            listOf(channel),
            { null },
            { Message(Message.Text("alert")) },
            { null },
            { Message(Message.Text("certificate")) },
            VerificationNoticeFormatter(NoticeLanguage.RU, ConsoleUrl.parse(CONSOLE)),
            RetryPolicy(RetrySettings()),
            QueueSettings(batch = 10, lease = Duration.ofMinutes(5), ttl = Duration.ofHours(24)),
            clock,
        )
    }
}
