// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.notify

import io.micrometer.core.instrument.simple.SimpleMeterRegistry
import org.junit.jupiter.api.Test
import org.junit.jupiter.api.extension.ExtendWith
import org.springframework.boot.test.system.CapturedOutput
import org.springframework.boot.test.system.OutputCaptureExtension
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

private class NamedChannel(
    override val name: String,
) : NotificationChannel {
    override fun send(message: Message) = SendOutcome.Delivered
}

private val SAY_NOTHING = NotificationFormatter { null }

/** S9a, test strategy 6 and metrics: what the queue works with, and what it counts. */
@ExtendWith(OutputCaptureExtension::class)
class NotifySetupTest {
    @Test
    fun `without a channel nothing is active and the log says so`(output: CapturedOutput) {
        assertEquals(emptyList(), activeChannels(emptyList(), SAY_NOTHING))
        assertTrue("Notifications are off: no channel (SARD_TELEGRAM_BOT_TOKEN, SARD_TELEGRAM_CHAT_ID)" in output.out)
    }

    @Test
    fun `without a formatter nothing is active and the log names S9b`(output: CapturedOutput) {
        assertEquals(emptyList(), activeChannels(listOf(NamedChannel("telegram")), formatter = null))
        assertTrue("Notifications are off: no NotificationFormatter bean (S9b)" in output.out)
    }

    @Test
    fun `with both every channel is active`(output: CapturedOutput) {
        val channels = listOf(NamedChannel("telegram"), NamedChannel("webhook"))
        assertEquals(channels, activeChannels(channels, SAY_NOTHING))
        assertTrue("Notifications go through [telegram, webhook]" in output.out)
        assertFalse("Notifications are off" in output.out)
    }

    @Test
    fun `metrics count per channel and reason`() {
        val meters = SimpleMeterRegistry()
        val metrics = MicrometerNotifyMetrics(meters)

        metrics.sent("telegram")
        metrics.sent("telegram")
        metrics.sent("webhook")
        metrics.retried("telegram")
        metrics.undelivered("telegram", "failed")
        metrics.undelivered("telegram", "expired")
        metrics.undelivered("telegram", "expired")
        metrics.pending(5)

        fun count(
            name: String,
            vararg tags: String,
        ) = meters
            .get(name)
            .tags(*tags)
            .counter()
            .count()
        assertEquals(2.0, count("sard.notify.sent", "channel", "telegram"))
        assertEquals(1.0, count("sard.notify.sent", "channel", "webhook"))
        assertEquals(1.0, count("sard.notify.retries", "channel", "telegram"))
        assertEquals(1.0, count("sard.notify.undelivered", "channel", "telegram", "reason", "failed"))
        assertEquals(2.0, count("sard.notify.undelivered", "channel", "telegram", "reason", "expired"))
        assertEquals(5.0, meters.get("sard.notify.pending").gauge().value())
    }

    @Test
    fun `the no-op metrics accept everything`() {
        NotifyMetrics.NONE.sent("telegram")
        NotifyMetrics.NONE.retried("telegram")
        NotifyMetrics.NONE.undelivered("telegram", "failed")
        NotifyMetrics.NONE.pending(1)
    }
}
