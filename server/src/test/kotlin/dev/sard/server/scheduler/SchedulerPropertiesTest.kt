// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.scheduler

import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import java.time.Duration
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith

/** `sard.scheduler.*`: the defaults of F3a, and a setting the scheduler cannot work with stops the start. */
@MutFlowTest
class SchedulerPropertiesTest {
    @Test
    fun `the defaults space catch-ups ten seconds apart, alert at the third skip and take 500 per tick`() {
        val properties = SchedulerProperties()
        assertEquals(Duration.ofSeconds(10), properties.interval)
        val expected = SchedulerSettings(Duration.ofSeconds(10), 3, 500)
        assertEquals(expected, MutFlow.underTest { properties.settings() })
    }

    @Test
    fun `the smallest settings that work are accepted`() {
        val properties = SchedulerProperties(Duration.ofMillis(1), Duration.ZERO, 1, 1)
        assertEquals(SchedulerSettings(Duration.ZERO, 1, 1), MutFlow.underTest { properties.settings() })
    }

    @Test
    fun `a setting out of range is refused, naming it`() {
        val refused =
            mapOf(
                SchedulerProperties(interval = Duration.ZERO) to "sard.scheduler.interval must be positive",
                SchedulerProperties(catchUpSpacing = Duration.ofSeconds(-1)) to
                    "sard.scheduler.catch-up-spacing must not be negative",
                SchedulerProperties(skipAlertThreshold = 0) to "sard.scheduler.skip-alert-threshold must be at least 1",
                SchedulerProperties(batch = 0) to "sard.scheduler.batch must be at least 1",
            )
        for ((properties, message) in refused) {
            val e = assertFailsWith<IllegalArgumentException> { MutFlow.underTest { properties.settings() } }
            assertEquals(message, e.message)
        }
    }
}
