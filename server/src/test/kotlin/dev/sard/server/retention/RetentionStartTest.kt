// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.retention

import org.springframework.boot.test.context.runner.ApplicationContextRunner
import java.time.Duration
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNotNull
import kotlin.test.assertTrue

/** Starting the server with `sard.retention.*` and `sard.notify.ttl` as the operator sets them. */
class RetentionStartTest {
    private val runner = ApplicationContextRunner().withUserConfiguration(RetentionSettingsConfiguration::class.java)

    private fun refusal(vararg properties: String): String {
        var message = ""
        runner.withPropertyValues(*properties).run { context ->
            val failure = assertNotNull(context.startupFailure, "the start must be refused")
            message = generateSequence<Throwable>(failure) { it.cause }.joinToString(" / ") { it.message.orEmpty() }
        }
        return message
    }

    @Test
    fun `Неположительный срок не даёт серверу стартовать`() {
        val refused =
            mapOf(
                "sard.retention.step-logs" to "0s",
                "sard.retention.runs" to "-1d",
                "sard.retention.verifications" to "0s",
                "sard.retention.schedule-fires" to "-1h",
                "sard.retention.notification-deliveries" to "0s",
                "sard.retention.interval" to "0s",
                "sard.retention.batch-size" to "0",
            )
        for ((property, value) in refused) {
            assertTrue(property in refusal("$property=$value"), property)
        }
    }

    @Test
    fun `Срок доставок не больше ttl уведомлений не даёт серверу стартовать`() {
        for (value in listOf("24h", "23h")) {
            val message = refusal("sard.notify.ttl=24h", "sard.retention.notification-deliveries=$value")
            assertTrue("sard.retention.notification-deliveries" in message && "sard.notify.ttl" in message, message)
        }
    }

    @Test
    fun `Срок доставок чуть больше ttl принимается, сроки по умолчанию`() {
        runner.withPropertyValues("sard.notify.ttl=24h", "sard.retention.notification-deliveries=25h").run { context ->
            assertEquals(Duration.ofHours(25), context.getBean(RetentionSettings::class.java).notificationDeliveries)
        }
        runner.run { context ->
            val settings = context.getBean(RetentionSettings::class.java)
            assertEquals(
                listOf(30L, 180L, 180L, 180L, 30L),
                listOf(
                    settings.stepLogs,
                    settings.runs,
                    settings.verifications,
                    settings.scheduleFires,
                    settings.notificationDeliveries,
                ).map { it.toDays() },
            )
        }
    }
}
