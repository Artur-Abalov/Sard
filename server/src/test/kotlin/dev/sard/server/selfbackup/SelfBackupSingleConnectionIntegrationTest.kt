// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.selfbackup

import dev.sard.server.TestcontainersConfiguration
import dev.sard.server.completeOnboarding
import dev.sard.server.pki.MovableClock
import dev.sard.server.runs.RUNS_NOW
import dev.sard.server.runs.RunsTestConfiguration
import dev.sard.server.runs.SystemRole
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.context.annotation.Import
import org.springframework.jdbc.core.JdbcTemplate
import java.time.Duration
import kotlin.test.AfterTest
import kotlin.test.BeforeTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

/**
 * ADR 0013: a request holds one connection at a time. Binding, reading and confirming the key copy ask whether the
 * onboarding is over, and must do it outside the tenant session: with a pool of one connection a second, nested
 * request for a connection waits out its timeout and fails.
 */
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.NONE,
    properties =
        [
            "spring.grpc.server.port=0",
            "sard.scheduler.interval=1h",
            "spring.datasource.hikari.maximum-pool-size=1",
            "spring.datasource.hikari.connection-timeout=2000",
        ],
)
@Import(TestcontainersConfiguration::class, RunsTestConfiguration::class)
class SelfBackupSingleConnectionIntegrationTest(
    @Autowired private val selfBackups: SelfBackups,
    @Autowired private val clock: MovableClock,
    @Autowired private val jdbc: JdbcTemplate,
) {
    private val tenant = SelfBackupTenant(jdbc)

    @BeforeTest
    fun `a tenant with its built-in agent`() {
        clock.now = RUNS_NOW
        tenant.create()
        jdbc.update("delete from onboarding_steps where step = 'keys_confirmed'")
    }

    @AfterTest
    fun `drop the tenant, the onboarding is complete again`() {
        tenant.drop()
        completeOnboarding(jdbc)
    }

    @Test
    fun `Привязка, чтение и подтверждение ключей обходятся одним соединением пула`() {
        val bound = selfBackups.bind(tenant.id, S3_REPOSITORY, confirmLocalStorage = false)
        assertTrue(bound.configured)
        clock.now = RUNS_NOW + Duration.ofHours(1)
        val runs = selfBackups.runNow(tenant.id)
        for (run in runs) tenant.finish(run.runId, "succeeded", clock.instant())
        assertEquals(listOf(SystemRole.SELF_DATABASE, SystemRole.SELF_KEYS), runs.map { it.role })

        assertTrue(selfBackups.get(tenant.id).firstBackupDone)
        val confirmed = selfBackups.confirmKeys(tenant.id, clock.instant())

        assertTrue(confirmed.recorded)
    }
}
