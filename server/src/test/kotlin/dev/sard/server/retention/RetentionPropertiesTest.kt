// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.retention

import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import java.time.Duration
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertTrue

private val TTL: Duration = Duration.ofHours(24)

/** `sard.retention.*`: the defaults and the settings the server refuses to start with. */
@MutFlowTest
class RetentionPropertiesTest {
    @Test
    fun `Сроки по умолчанию`() {
        val settings = MutFlow.underTest { RetentionProperties().settings(TTL) }

        assertEquals(Duration.ofDays(30), settings.stepLogs)
        assertEquals(Duration.ofDays(180), settings.runs)
        assertEquals(Duration.ofDays(180), settings.verifications)
        assertEquals(Duration.ofDays(180), settings.scheduleFires)
        assertEquals(Duration.ofDays(30), settings.notificationDeliveries)
        assertEquals(Duration.ofHours(1), settings.interval)
        assertEquals(500, settings.batchSize)
    }

    @Test
    fun `Наименьшие допустимые интервал и размер пачки принимаются`() {
        val properties = RetentionProperties(interval = Duration.ofMillis(1), batchSize = 1)

        val settings = MutFlow.underTest { properties.settings(TTL) }

        assertEquals(Duration.ofMillis(1), settings.interval)
        assertEquals(1, settings.batchSize)
    }

    @Test
    fun `Неположительный срок не даёт серверу стартовать`() {
        val refused =
            mapOf(
                RetentionProperties(stepLogs = Duration.ZERO) to "sard.retention.step-logs",
                RetentionProperties(runs = Duration.ofDays(-1)) to "sard.retention.runs",
                RetentionProperties(verifications = Duration.ZERO) to "sard.retention.verifications",
                RetentionProperties(scheduleFires = Duration.ofHours(-1)) to "sard.retention.schedule-fires",
                RetentionProperties(notificationDeliveries = Duration.ZERO) to "sard.retention.notification-deliveries",
            )
        for ((properties, name) in refused) {
            val e = assertFailsWith<IllegalArgumentException> { MutFlow.underTest { properties.settings(TTL) } }
            assertEquals("$name must be positive", e.message)
        }
    }

    @Test
    fun `Неположительный интервал или размер пачки не даёт серверу стартовать`() {
        val interval =
            assertFailsWith<IllegalArgumentException> {
                MutFlow.underTest { RetentionProperties(interval = Duration.ZERO).settings(TTL) }
            }
        val batch =
            assertFailsWith<IllegalArgumentException> {
                MutFlow.underTest { RetentionProperties(batchSize = 0).settings(TTL) }
            }

        assertEquals("sard.retention.interval must be positive", interval.message)
        assertEquals("sard.retention.batch-size must be at least 1", batch.message)
    }

    @Test
    fun `Срок доставок не больше ttl уведомлений не даёт серверу стартовать`() {
        for (hours in listOf(24L, 23L)) {
            val properties = RetentionProperties(notificationDeliveries = Duration.ofHours(hours))
            val e = assertFailsWith<IllegalArgumentException> { MutFlow.underTest { properties.settings(TTL) } }
            val message = e.message.orEmpty()
            assertTrue("sard.retention.notification-deliveries" in message && "sard.notify.ttl" in message, message)
        }
    }

    @Test
    fun `Срок доставок чуть больше ttl принимается`() {
        val properties = RetentionProperties(notificationDeliveries = Duration.ofHours(25))

        val settings = MutFlow.underTest { properties.settings(TTL) }

        assertEquals(Duration.ofHours(25), settings.notificationDeliveries)
    }
}
