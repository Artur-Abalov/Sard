// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.retention

import dev.sard.server.TestcontainersConfiguration
import dev.sard.server.notify.NotificationLoop
import dev.sard.server.notify.NotificationService
import dev.sard.server.notify.telegram.FakeBotApi
import dev.sard.server.notify.telegram.TEST_TOKEN
import dev.sard.server.persistence.StepLogPartitions
import dev.sard.server.pki.MovableClock
import dev.sard.server.runs.RUNS_NOW
import dev.sard.server.runs.RunsTestConfiguration
import org.junit.jupiter.api.AfterAll
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.context.annotation.Import
import org.springframework.jdbc.core.JdbcTemplate
import org.springframework.test.context.DynamicPropertyRegistry
import org.springframework.test.context.DynamicPropertySource
import java.time.Duration
import kotlin.test.AfterTest
import kotlin.test.BeforeTest
import kotlin.test.Test
import kotlin.test.assertEquals

/** The cleaning and the notification planner share a window: a delivery gone before its time would be made again. */
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.NONE,
    properties = [
        "spring.grpc.server.port=0",
        "sard.notify.tick-interval=1h",
        "sard.notify.ttl=24h",
        "sard.scheduler.interval=1h",
        "sard.freshness.interval=1h",
        "sard.verification.interval=1h",
        "SARD_TELEGRAM_BOT_TOKEN=$TEST_TOKEN",
        "SARD_TELEGRAM_CHAT_ID=-100777",
    ],
)
@Import(TestcontainersConfiguration::class, RunsTestConfiguration::class)
class RetentionNotificationsIntegrationTest(
    @Autowired private val retention: RetentionPass,
    @Autowired private val service: NotificationService,
    @Autowired private val loop: NotificationLoop,
    @Autowired private val logPartitions: StepLogPartitions,
    @Autowired private val clock: MovableClock,
    @Autowired private val jdbc: JdbcTemplate,
) {
    private val tenant = SceneTenant(jdbc)

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
    fun `the moment of the pass`() {
        loop.stop()
        clock.now = T0
        fake.requests.clear()
        fake.fallback = FakeBotApi.ok()
    }

    @AfterTest
    fun `drop the tenant`() {
        tenant.drop()
        restorePartitions(logPartitions)
        clock.now = RUNS_NOW
    }

    @Test
    fun `Доставка старше ttl, но моложе своего срока, остаётся, и второй планировщик не создаёт`() {
        val source = tenant.source()
        val run = tenant.run(source, T0.minus(Duration.ofDays(29)), status = "succeeded")
        val delivery = tenant.delivery(run.id, T0.minus(Duration.ofDays(29)))

        retention.run()
        service.tick()

        assertEquals(
            listOf(delivery),
            jdbc.queryForList(
                "select id from notification_deliveries where tenant_id = ?",
                java.util.UUID::class.java,
                tenant.id,
            ),
        )
        assertEquals(0, fake.requests.size)
    }

    @Test
    fun `Удалённая доставка не создаётся заново планировщиком`() {
        val source = tenant.source()
        val run = tenant.run(source, T0.minus(Duration.ofDays(31)), status = "failed")
        tenant.delivery(run.id, T0.minus(Duration.ofDays(31)))

        retention.run()
        service.tick()

        assertEquals(0, tenant.count("notification_deliveries"))
        assertEquals(0, fake.requests.size)
    }
}
