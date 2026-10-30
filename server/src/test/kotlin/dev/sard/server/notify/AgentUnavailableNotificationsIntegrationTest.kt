// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.notify

import dev.sard.server.TestcontainersConfiguration
import dev.sard.server.notify.telegram.FakeBotApi
import dev.sard.server.notify.telegram.TEST_TOKEN
import dev.sard.server.persistence.TenantSessions
import dev.sard.server.pki.MovableClock
import dev.sard.server.runs.AgentWaits
import dev.sard.server.runs.RUNS_NOW
import dev.sard.server.runs.RunAnnouncer
import dev.sard.server.runs.RunEvents
import dev.sard.server.runs.Runs
import dev.sard.server.runs.RunsTenant
import dev.sard.server.runs.RunsTestConfiguration
import dev.sard.server.runs.Sources
import dev.sard.server.runs.StepDeadlines
import dev.sard.server.runs.StepOutput
import dev.sard.server.runs.StepReport
import dev.sard.server.runs.StepResults
import dev.sard.server.runs.StepState
import dev.sard.server.runs.StepTransitions
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
import kotlin.test.assertTrue

private const val CHAT = "-100778"
private const val REPOSITORY_ID = "5f0c3e2d9a8b7c6d5e4f3a2b1c0d9e8f7a6b5c4d3e2f1a0b9c8d7e6f5a4b3c2d"
private val SNAPSHOT = StepOutput.Backup("4a3b2c1d", 1_610_612_736, 12_897_485, REPOSITORY_ID)
private val SUCCEEDED = StepReport(StepState.SUCCEEDED, null, SNAPSHOT)
private val FAILED = StepReport(StepState.FAILED, "disk full", null)

/**
 * J1c, the @server scenarios: a run that was not run because its agent was unavailable reaches a fake Telegram
 * once, names the agent's last contact, and a series of scheduled ones is told by its first (ДJ2).
 */
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.NONE,
    properties = [
        "spring.grpc.server.port=0",
        "sard.notify.tick-interval=1h",
        "SARD_TELEGRAM_BOT_TOKEN=$TEST_TOKEN",
        "SARD_TELEGRAM_CHAT_ID=$CHAT",
        "SARD_NOTIFY_LANGUAGE=ru",
    ],
)
@Import(TestcontainersConfiguration::class, RunsTestConfiguration::class)
class AgentUnavailableNotificationsIntegrationTest(
    @Autowired private val service: NotificationService,
    @Autowired private val loop: NotificationLoop,
    @Autowired private val sources: Sources,
    @Autowired private val runs: Runs,
    @Autowired private val steps: StepTransitions,
    @Autowired private val results: StepResults,
    @Autowired private val clock: MovableClock,
    @Autowired private val jdbc: JdbcTemplate,
    @Autowired private val sessions: TenantSessions,
    @Autowired private val deadlines: StepDeadlines,
    @Autowired private val announcer: RunAnnouncer,
    @Autowired private val events: RunEvents,
) {
    private val tenant = RunsTenant(jdbc)
    private val lastContact = Instant.parse("2026-10-01T09:00:00Z")

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

    private fun shown(index: Int = 0): String = fake.shownText(index)

    private fun status(run: UUID) =
        jdbc.queryForObject("select status from notification_deliveries where run_id = ?", String::class.java, run)

    private fun ticks() = repeat(3) { service.tick() }

    private fun seenAt(at: Instant?) {
        jdbc.update("update agents set last_seen_at = ? where id = ?", at?.let(Timestamp::from), tenant.agentId)
    }

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

    /** [sourceId]'s run, queued [hour] hours after T0, whose wait for the offline agent ended an hour later. */
    private fun unavailable(
        sourceId: UUID,
        hour: Long,
        scheduled: Boolean = false,
    ): UUID {
        clock.now = RUNS_NOW.plusSeconds(3600 * hour)
        val run = runs.start(tenant.id, sourceId)
        if (scheduled) jdbc.update("update runs set trigger = 'schedule' where id = ?", run.id)
        val deadline = Timestamp.from(clock.now.plusSeconds(3600))
        jdbc.update("update runs set agent_wait_until = ? where id = ?", deadline, run.id)
        clock.now = clock.now.plusSeconds(3600)
        val start = RUNS_NOW.minus(Duration.ofDays(1))
        val waits = AgentWaits(deadlines, sessions, clock, announcer, events, { false }, start, Duration.ZERO)
        assertEquals(1, waits.expire())
        ticks()
        return run.id
    }

    // J1c: Уведомление о недоступном агенте называет последнюю связь
    @Test
    fun `Недоступный агент даёт одно сообщение с последней связью`() {
        seenAt(lastContact)
        val run = unavailable(sources.create(tenant.id, tenant.draft("db-main")).id, 1)

        assertEquals("delivered", status(run))
        assertEquals(1, fake.requests.size)
        assertEquals("⏸ Бэкап не выполнен — агент был недоступен: db-main", shown().lines().first())
        assertTrue("Агент не на связи с 2026-10-01 09:00 UTC" in shown().lines())
    }

    // J1c: Уведомление об агенте, который ни разу не подключался, так и говорит
    @Test
    fun `Агент, который ни разу не подключался, назван в сообщении`() {
        unavailable(sources.create(tenant.id, tenant.draft("db-main")).id, 1)

        assertTrue("Агент ещё ни разу не подключался" in shown().lines())
    }

    // J1c: Второй подряд недоступный агент по расписанию не уведомляет
    @Test
    fun `Второй подряд недоступный агент по расписанию не уведомляет, а после другого итога уведомляет снова`() {
        val source = sources.create(tenant.id, tenant.draft("db-main")).id

        val first = unavailable(source, 1, scheduled = true)
        val second = unavailable(source, 3, scheduled = true)
        scheduled(source, SUCCEEDED, 5)
        val again = unavailable(source, 7, scheduled = true)

        assertEquals(listOf("delivered", "skipped", "delivered"), listOf(first, second, again).map(::status))
    }

    // ДJ12: Недоступный агент прерывает счёт ошибок подряд
    @Test
    fun `Недоступный агент прерывает счёт ошибок подряд`() {
        val source = sources.create(tenant.id, tenant.draft("db-main")).id

        scheduled(source, FAILED, 0)
        unavailable(source, 2, scheduled = true)
        scheduled(source, FAILED, 4)
        val back = scheduled(source, SUCCEEDED, 5)

        assertEquals("delivered", status(back))
        assertTrue("Ошибок подряд перед этим: 1" in shown(fake.requests.size - 1).lines())
    }
}
