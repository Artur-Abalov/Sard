// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.onboarding

import dev.sard.server.TestcontainersConfiguration
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.context.annotation.Import
import org.springframework.jdbc.core.JdbcTemplate
import java.time.Instant
import kotlin.test.AfterTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

private val NOW: Instant = Instant.parse("2026-10-09T12:00:00Z")

/** The installation-wide steps of the first start in the database (migration V202610101200). */
@SpringBootTest(properties = ["spring.grpc.server.port=0", "server.port=0"])
@Import(TestcontainersConfiguration::class)
class JdbcOnboardingStepsIntegrationTest(
    @Autowired private val jdbc: JdbcTemplate,
) {
    private val steps = JdbcOnboardingSteps(jdbc)

    @AfterTest
    fun `forget the state`() {
        jdbc.update("delete from onboarding_steps")
    }

    @Test
    fun `Шаг ca подтверждается один раз и остаётся`() {
        assertFalse(steps.caConfirmed())

        assertTrue(steps.confirmCa(NOW))
        assertFalse(steps.confirmCa(NOW.plusSeconds(5)))

        assertTrue(steps.caConfirmed())
        assertEquals(1, jdbc.queryForObject("select count(*) from onboarding_steps", Int::class.java))
    }
}
