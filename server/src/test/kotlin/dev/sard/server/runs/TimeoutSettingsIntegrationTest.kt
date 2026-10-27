// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.runs

import dev.sard.server.TestcontainersConfiguration
import dev.sard.server.pki.MovableClock
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.context.annotation.Import
import org.springframework.jdbc.core.JdbcTemplate
import kotlin.test.AfterTest
import kotlin.test.BeforeTest
import kotlin.test.Test
import kotlin.test.assertEquals

/** The default step timeouts of a tenant (ДJ8): 360 and 720 minutes until set, then what the tenant saved. */
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.NONE,
    properties = ["spring.grpc.server.port=0"],
)
@Import(TestcontainersConfiguration::class, RunsTestConfiguration::class)
class TimeoutSettingsIntegrationTest(
    @Autowired private val settings: TimeoutSettings,
    @Autowired private val clock: MovableClock,
    @Autowired private val jdbc: JdbcTemplate,
) {
    private val tenant = RunsTenant(jdbc)
    private val other = RunsTenant(jdbc)

    @BeforeTest
    fun `two tenants`() {
        clock.now = RUNS_NOW
        tenant.create()
        other.create()
    }

    @AfterTest
    fun `drop both tenants`() {
        tenant.drop()
        other.drop()
    }

    // Сценарий: Новая установка отдаёт сроки по умолчанию 6 и 12 часов
    @Test
    fun `a tenant that saved nothing has 6 hours for backups and 12 for restores`() {
        assertEquals(TimeoutDefaults(360, 720), settings.get(tenant.id))
    }

    @Test
    fun `saved defaults are read back, replaced by the next save and kept per tenant`() {
        assertEquals(TimeoutDefaults(480, 600), settings.set(tenant.id, TimeoutDefaults(480, 600)))
        assertEquals(TimeoutDefaults(480, 600), settings.get(tenant.id))

        settings.set(tenant.id, TimeoutDefaults(1, 1440))

        assertEquals(TimeoutDefaults(1, 1440), settings.get(tenant.id))
        assertEquals(TimeoutDefaults(360, 720), settings.get(other.id))
    }
}
