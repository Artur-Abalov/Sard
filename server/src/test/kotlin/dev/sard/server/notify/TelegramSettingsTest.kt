// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.notify

import dev.sard.server.notify.telegram.TEST_TOKEN
import dev.sard.server.notify.telegram.TelegramChannel
import org.junit.jupiter.api.Test
import tools.jackson.databind.json.JsonMapper
import java.time.Duration
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertFalse
import kotlin.test.assertIs
import kotlin.test.assertNull
import kotlin.test.assertTrue

private val JSON = JsonMapper.builder().build()
private val PROPERTIES = TelegramProperties()
private const val NEEDS = "Telegram needs SARD_TELEGRAM_BOT_TOKEN and SARD_TELEGRAM_CHAT_ID;"

/** OQ-017 at stage 1: the bot comes from the environment; none at all is allowed, half is not. */
class TelegramSettingsTest {
    @Test
    fun `no token and no chat means no Telegram channel`() {
        assertNull(telegramChannel(TelegramCredentials("", "  "), PROPERTIES, JSON))
    }

    @Test
    fun `a token and a chat make the Telegram channel`() {
        val credentials = TelegramCredentials(" $TEST_TOKEN ", "-100")
        val channel = assertIs<TelegramChannel>(telegramChannel(credentials, PROPERTIES, JSON))
        assertEquals("telegram", channel.name)
    }

    @Test
    fun `a token without a chat is refused without echoing the token`() {
        val error = assertFailsWith<IllegalArgumentException> { TelegramCredentials(TEST_TOKEN, "").configured() }
        assertEquals("$NEEDS SARD_TELEGRAM_CHAT_ID is not set", error.message)
    }

    @Test
    fun `a chat without a token is refused`() {
        val error = assertFailsWith<IllegalArgumentException> { TelegramCredentials("", "-100").configured() }
        assertEquals("$NEEDS SARD_TELEGRAM_BOT_TOKEN is not set", error.message)
    }

    @Test
    fun `the credentials never print the token`() {
        val text = TelegramCredentials(TEST_TOKEN, "-100").toString()
        assertFalse(TEST_TOKEN in text, text)
        assertTrue("-100" in text, text)
    }

    @Test
    fun `timeouts must be positive`() {
        val credentials = TelegramCredentials(TEST_TOKEN, "-100")
        assertFailsWith<IllegalArgumentException> {
            telegramChannel(credentials, TelegramProperties(connectTimeout = Duration.ZERO), JSON)
        }
        assertFailsWith<IllegalArgumentException> {
            telegramChannel(credentials, TelegramProperties(requestTimeout = Duration.ZERO), JSON)
        }
    }

    @Test
    fun `queue settings are validated`() {
        assertFailsWith<IllegalArgumentException> { NotifyProperties(batch = 0).queue() }
        assertFailsWith<IllegalArgumentException> { NotifyProperties(lease = Duration.ZERO).queue() }
        assertFailsWith<IllegalArgumentException> { NotifyProperties(tickInterval = Duration.ZERO).interval() }
        assertEquals(QueueSettings(100, Duration.ofMinutes(5), Duration.ofHours(24)), NotifyProperties().queue())
        assertEquals(Duration.ofSeconds(10), NotifyProperties().interval())
    }
}
