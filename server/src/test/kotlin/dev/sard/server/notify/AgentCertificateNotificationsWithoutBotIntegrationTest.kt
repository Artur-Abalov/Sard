// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.notify

import dev.sard.server.TestcontainersConfiguration
import dev.sard.server.fleet.AgentCertificateCheck
import dev.sard.server.pki.MovableClock
import dev.sard.server.runs.RunsTenant
import dev.sard.server.runs.RunsTestConfiguration
import org.junit.jupiter.api.extension.ExtendWith
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.boot.test.system.CapturedOutput
import org.springframework.boot.test.system.OutputCaptureExtension
import org.springframework.context.annotation.Import
import org.springframework.jdbc.core.JdbcTemplate
import java.sql.Timestamp
import java.time.Duration
import java.time.Instant
import kotlin.test.AfterTest
import kotlin.test.Test
import kotlin.test.assertEquals

private val T0: Instant = Instant.parse("2026-10-15T12:00:00Z")

/**
 * C1 phase 3, П44: without a bot the certificate is still checked and the event is recorded, but nothing is queued
 * for delivery and the log is quiet.
 */
@ExtendWith(OutputCaptureExtension::class)
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.NONE,
    // Its own context: the warning is logged while this class's context starts.
    properties = [
        "spring.grpc.server.port=0",
        "sard.notify.tick-interval=1h",
        "sard.pki.agent-certificate-check-interval=PT1H",
    ],
)
@Import(TestcontainersConfiguration::class, RunsTestConfiguration::class)
class AgentCertificateNotificationsWithoutBotIntegrationTest(
    @Autowired private val check: AgentCertificateCheck,
    @Autowired private val service: NotificationService,
    @Autowired private val clock: MovableClock,
    @Autowired private val jdbc: JdbcTemplate,
) {
    private val tenant = RunsTenant(jdbc).create()

    @AfterTest
    fun `drop the tenant`() {
        for (table in listOf("notification_deliveries", "agent_certificate_events", "agent_certificates")) {
            jdbc.update("delete from $table where tenant_id = ?", tenant.id)
        }
        tenant.drop()
    }

    @Test
    fun `Без бота событий в очереди доставки нет и журнал не шумит`(output: CapturedOutput) {
        jdbc.update(
            "insert into agent_certificates (serial, tenant_id, agent_id, issued_at, not_after) values (?, ?, ?, ?, ?)",
            "a".repeat(32),
            tenant.id,
            tenant.agentId,
            Timestamp.from(T0),
            Timestamp.from(T0 + Duration.ofDays(365)),
        )
        clock.now = T0 + Duration.ofDays(335)

        check.tick()
        repeat(3) { service.tick() }

        val deliveries = jdbc.queryForObject("select count(*) from notification_deliveries", Int::class.java)
        assertEquals(0, deliveries)
        val problems = output.all.lines().filter { (" WARN " in it || " ERROR " in it) && ".server.notify." in it }
        assertEquals(1, problems.size, "only the startup warning of the notification code: $problems")
        assertEquals(1, problems.count { "Notifications are off" in it })
    }
}
