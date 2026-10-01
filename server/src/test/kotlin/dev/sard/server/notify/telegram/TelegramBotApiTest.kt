// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.notify.telegram

import dev.sard.server.notify.SendOutcome
import org.junit.jupiter.api.AfterEach
import org.junit.jupiter.api.Test
import tools.jackson.databind.json.JsonMapper
import java.net.ServerSocket
import java.net.URI
import java.net.http.HttpClient
import java.time.Duration
import java.util.concurrent.CountDownLatch
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertIs
import kotlin.test.assertTrue

private val JSON = JsonMapper.builder().build()
private const val CHAT = "-1001234567890"

/** sendMessage over HTTP (S9a, test strategy 1): what each Bot API answer means for a retry. */
class TelegramBotApiTest {
    private val fake = FakeBotApi()
    private val http = HttpClient.newBuilder().connectTimeout(Duration.ofSeconds(5)).build()

    @AfterEach
    fun close() {
        fake.close()
    }

    private fun api(
        uri: URI = fake.uri,
        timeout: Duration = Duration.ofSeconds(5),
    ) = TelegramBotApi(http, uri, BotToken(TEST_TOKEN), timeout, JSON)

    @Test
    fun `a message is posted as HTML to the bot's sendMessage`() {
        assertEquals(SendOutcome.Delivered, api().sendMessage(CHAT, "<b>ok</b> &amp; done"))

        val request = fake.requests.single()
        assertEquals("POST", request.method)
        assertEquals("/bot$TEST_TOKEN/sendMessage", request.path)
        assertEquals("application/json", request.contentType)
        val body = JSON.readTree(request.body)
        assertEquals(CHAT, body.path("chat_id").asString())
        assertEquals("<b>ok</b> &amp; done", body.path("text").asString())
        assertEquals("HTML", body.path("parse_mode").asString())
        assertEquals(true, body.path("link_preview_options").path("is_disabled").asBoolean())
    }

    @Test
    fun `429 asks to wait retry_after`() {
        fake.reply(FakeBotApi.tooMany(37))
        assertEquals(SendOutcome.RetryAfter(Duration.ofSeconds(37)), api().sendMessage(CHAT, "x"))
    }

    @Test
    fun `429 without retry_after is a transient failure`() {
        fake.reply(FakeBotApi.error(429, "Too Many Requests"))
        assertEquals(SendOutcome.Transient("HTTP 429: Too Many Requests"), api().sendMessage(CHAT, "x"))
    }

    @Test
    fun `5xx is a transient failure`() {
        fake.reply(FakeBotApi.error(502, "Bad Gateway"))
        assertEquals(SendOutcome.Transient("HTTP 502: Bad Gateway"), api().sendMessage(CHAT, "x"))
    }

    @Test
    fun `a 5xx without a JSON body still names the status`() {
        fake.reply(BotReply(500, "<html>oops</html>"))
        assertEquals(SendOutcome.Transient("HTTP 500"), api().sendMessage(CHAT, "x"))
    }

    @Test
    fun `4xx other than 429 is rejected with Telegram's description`() {
        fake.reply(FakeBotApi.error(400, "Bad Request: chat not found"))
        assertEquals(SendOutcome.Rejected("HTTP 400: Bad Request: chat not found"), api().sendMessage(CHAT, "x"))
    }

    @Test
    fun `a 200 that is not ok is rejected`() {
        fake.reply(BotReply(200, """{"ok":false,"description":"strange"}"""))
        assertEquals(SendOutcome.Rejected("HTTP 200: strange"), api().sendMessage(CHAT, "x"))
    }

    @Test
    fun `a long description is cut`() {
        fake.reply(FakeBotApi.error(400, "x".repeat(2000)))
        val outcome = assertIs<SendOutcome.Rejected>(api().sendMessage(CHAT, "x"))
        assertEquals(MAX_REASON_CHARS, outcome.reason.length)
    }

    @Test
    fun `a refused connection is a transient network failure`() {
        val closed = ServerSocket(0).use { it.localPort }
        val outcome = api(URI.create("http://127.0.0.1:$closed")).sendMessage(CHAT, "x")
        val transient = assertIs<SendOutcome.Transient>(outcome)
        assertTrue(transient.reason.startsWith("network: ConnectException"), transient.reason)
    }

    @Test
    fun `no answer within the request timeout is a transient failure`() {
        val held = CountDownLatch(1)
        fake.reply(BotReply(200, "{}", hold = held))
        try {
            assertEquals(SendOutcome.Transient("timeout"), api(timeout = Duration.ofMillis(200)).sendMessage(CHAT, "x"))
        } finally {
            held.countDown()
        }
    }

    @Test
    fun `a token in the wrong format is refused without echoing it`() {
        val error = assertFailsWith<IllegalArgumentException> { BotToken("not a token") }
        assertTrue("not a token" !in error.message.orEmpty(), error.message)
    }
}
