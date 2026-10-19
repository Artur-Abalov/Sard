// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.verify

import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import java.time.Duration
import java.time.ZoneId
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith

/** `sard.verification.*`: the defaults of the owner's decision 7, and settings out of range refused by name. */
@MutFlowTest
class VerificationPropertiesTest {
    @Test
    fun `the defaults are a nightly cycle in the installation's zone, twice the snapshot plus a GiB, six hours`() {
        val settings = MutFlow.underTest { VerificationProperties().settings() }

        assertEquals("30 4 * * *", settings.defaultCron)
        assertEquals(ZoneId.systemDefault().id, settings.defaultTimezone)
        assertEquals(SpaceNeed(2.0, 1L shl 30), settings.space)
        assertEquals(Duration.ofHours(6), settings.stepTimeout)
    }

    @Test
    fun `a zone that is set is kept, and the limits themselves are allowed`() {
        val properties = VerificationProperties(defaultTimezone = "Europe/Moscow", spaceFactor = 1.0, spaceHeadroom = 0)

        val settings = MutFlow.underTest { properties.settings() }

        assertEquals("Europe/Moscow", settings.defaultTimezone)
        assertEquals(SpaceNeed(1.0, 0), settings.space)
    }

    @Test
    fun `each setting out of range is refused with its name`() {
        val cases =
            mapOf(
                VerificationProperties(interval = Duration.ZERO) to "sard.verification.interval must be positive",
                VerificationProperties(spaceFactor = 0.99) to "sard.verification.space-factor must be at least 1",
                VerificationProperties(spaceHeadroom = -1) to "sard.verification.space-headroom must not be negative",
                VerificationProperties(stepTimeout = Duration.ZERO) to
                    "sard.verification.step-timeout must be positive",
            )
        for ((properties, message) in cases) {
            val error = assertFailsWith<IllegalArgumentException> { MutFlow.underTest { properties.settings() } }
            assertEquals(message, error.message)
        }
    }
}
