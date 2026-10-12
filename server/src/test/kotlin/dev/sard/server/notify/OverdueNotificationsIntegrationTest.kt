// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.notify

import dev.sard.server.TestcontainersConfiguration
import dev.sard.server.notify.telegram.FakeBotApi
import dev.sard.server.notify.telegram.TEST_TOKEN
import dev.sard.server.pki.MovableClock
import dev.sard.server.runs.RUNS_NOW
import dev.sard.server.runs.Runs
import dev.sard.server.runs.RunsTenant
import dev.sard.server.runs.RunsTestConfiguration
import dev.sard.server.runs.Sources
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import org.junit.jupiter.api.AfterAll
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.context.annotation.Import
import org.springframework.jdbc.core.JdbcTemplate
import org.springframework.test.context.DynamicPropertyRegistry
import org.springframework.test.context.DynamicPropertySource
import java.sql.Timestamp
import java.time.Duration
import java.time.Instant
import java.util.UUID
import kotlin.test.AfterTest
import kotlin.test.BeforeTest
import kotlin.test.Test
import kotlin.test.assertEquals

private const val CHAT = "-100778"
private const val CONSOLE = "https://sard.example.com"
private val LAST_SUCCESS: Instant = Instant.parse("2026-09-28T03:10:00Z")
private val DEADLINE: Instant = Instant.parse("2026-09-29T15:10:00Z")

/**
 * F6: an overdue episode is told about when it opens and when a success closes it, through the same queue as runs;
 * a closing run that is told about itself (a manual result, a recovery) carries the news alone.
 */
@MutFlowTest(includeTargets = [OverdueDeliveries::class, Notices::class, OverdueFormatter::class])
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.NONE,
    properties = [
        "spring.grpc.server.port=0",
        "sard.notify.tick-interval=1h",
        "sard.freshness.interval=1h",
        "SARD_TELEGRAM_BOT_TOKEN=$TEST_TOKEN",
        "SARD_TELEGRAM_CHAT_ID=$CHAT",
        "SARD_NOTIFY_LANGUAGE=ru",
        "SARD_CONSOLE_PUBLIC_URL=$CONSOLE",
    ],
)
@Import(TestcontainersConfiguration::class, RunsTestConfiguration::class)
class OverdueNotificationsIntegrationTest(
    @Autowired private val service: NotificationService,
    @Autowired private val loop: NotificationLoop,
    @Autowired private val sources: Sources,
    @Autowired private val runs: Runs,
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
        tenant.drop()
    }

    private fun ticks() = repeat(2) { MutFlow.underTest { service.tick() } }

    private fun source(name: String = "db-main"): UUID = sources.create(tenant.id, tenant.draft(name)).id

    private fun opened(
        source: UUID,
        countedFrom: Instant? = LAST_SUCCESS,
    ): UUID {
        val episode = UUID.randomUUID()
        jdbc.update(
            "insert into overdue_episodes (id, tenant_id, source_id, counted_from, deadline, opened_at) " +
                "values (?, ?, ?, ?, ?, ?)",
            episode,
            tenant.id,
            source,
            countedFrom?.let(Timestamp::from),
            Timestamp.from(DEADLINE),
            Timestamp.from(clock.now),
        )
        return episode
    }

    /** A succeeded run of [source] started by [trigger], finished now, that closes [episode]. */
    private fun closedBy(
        episode: UUID,
        source: UUID,
        trigger: String,
    ): UUID {
        val run = runs.start(tenant.id, source).id
        jdbc.update("update runs set trigger = ? where id = ?", trigger, run)
        tenant.finish(run)
        val now = Timestamp.from(clock.now)
        jdbc.update("update runs set finished_at = ? where id = ?", now, run)
        jdbc.update("update overdue_episodes set closed_at = ?, closed_by_run_id = ? where id = ?", now, run, episode)
        return run
    }

    private fun statuses(episode: UUID): Map<String, String> =
        jdbc
            .queryForList("select subject, status from notification_deliveries where episode_id = ?", episode)
            .associate { it["subject"] as String to it["status"] as String }

    @Test
    fun `Открытый эпизод даёт одно сообщение с последним успехом и сроком`() {
        val episode = opened(source())

        ticks()

        val expected =
            """
            ⚠️ Давно не было успешного бэкапа: db-main
            Агент: db1
            Последний успех: 2026-09-28 03:10 UTC
            Ожидался не позже: 2026-09-29 15:10 UTC
            """.trimIndent()
        assertEquals(listOf(expected), List(fake.requests.size) { fake.shownText(it) })
        assertEquals(mapOf("overdue" to "delivered"), statuses(episode))
    }

    @Test
    fun `Источник без единого успеха так и называется`() {
        opened(source(), countedFrom = null)

        ticks()

        assertEquals("Успешных бэкапов ещё не было", fake.shownText(0).lines()[2])
    }

    @Test
    fun `Тихий успех по расписанию закрывает эпизод сообщением о возобновлении`() {
        val source = source()
        val episode = opened(source)
        ticks()
        clock.now = RUNS_NOW.plus(Duration.ofHours(12))
        val run = closedBy(episode, source, "schedule")

        ticks()

        val expected =
            """
            ✅ Бэкапы возобновились: db-main
            Агент: db1
            Запуск: $run
            $CONSOLE/runs/$run
            """.trimIndent()
        assertEquals(expected, fake.shownText(1))
        assertEquals(2, fake.requests.size)
        assertEquals(mapOf("overdue" to "delivered", "overdue_over" to "delivered"), statuses(episode))
    }

    @Test
    fun `Ручной успех сообщает о себе сам, закрытие эпизода молчит`() {
        val source = source()
        val episode = opened(source)
        ticks()
        clock.now = RUNS_NOW.plus(Duration.ofHours(1))
        closedBy(episode, source, "manual")

        ticks()

        assertEquals(2, fake.requests.size)
        assertEquals("✅ Бэкап выполнен: db-main", fake.shownText(1).lines().first())
        assertEquals("skipped", statuses(episode)["overdue_over"])
    }

    @Test
    fun `Эпизод, закрытый без успеха, о закрытии не сообщает`() {
        val episode = opened(source())
        ticks()
        jdbc.update("update overdue_episodes set closed_at = ? where id = ?", Timestamp.from(clock.now), episode)

        ticks()

        assertEquals(1, fake.requests.size)
        assertEquals(mapOf("overdue" to "delivered"), statuses(episode))
    }
}
