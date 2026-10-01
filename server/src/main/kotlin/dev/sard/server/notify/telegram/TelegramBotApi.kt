// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.notify.telegram

import dev.sard.server.notify.SendOutcome
import tools.jackson.core.JacksonException
import tools.jackson.databind.ObjectMapper
import java.io.IOException
import java.net.URI
import java.net.URLEncoder
import java.net.http.HttpClient
import java.net.http.HttpRequest
import java.net.http.HttpResponse
import java.net.http.HttpTimeoutException
import java.time.Duration

/** Longest failure reason kept in `notification_deliveries.last_error`. */
internal const val MAX_REASON_CHARS = 500

private const val REDACTED = "[REDACTED]"
private val TOKEN_FORMAT = Regex("^[0-9]{1,20}:[A-Za-z0-9_-]{20,100}$")
private const val HTTP_OK = 200
private const val HTTP_TOO_MANY = 429
private const val HTTP_SERVER_ERROR = 500

/**
 * The bot token (OQ-017: from SARD_TELEGRAM_BOT_TOKEN). Telegram puts it in the request path, so
 * it stays inside this class and [TelegramBotApi]: [toString] hides it, and [scrub] removes it
 * from any text that leaves them.
 */
class BotToken(
    private val value: String,
) {
    init {
        require(TOKEN_FORMAT.matches(value)) { "SARD_TELEGRAM_BOT_TOKEN is not a bot token (digits:secret)" }
    }

    internal fun path(method: String) = "/bot$value/$method"

    private val encoded = URLEncoder.encode(value, Charsets.UTF_8)

    fun scrub(text: String): String = text.replace(value, REDACTED).replace(encoded, REDACTED)

    override fun toString() = "BotToken($REDACTED)"
}

/**
 * Telegram Bot API `sendMessage` over the JDK HTTP client. It never throws and never lets the
 * request, its URI or a response object out: an outcome carries only a status and Telegram's
 * description, both scrubbed of the token.
 */
class TelegramBotApi(
    private val http: HttpClient,
    private val baseUri: URI,
    private val token: BotToken,
    private val requestTimeout: Duration,
    private val json: ObjectMapper,
) {
    fun sendMessage(
        chatId: String,
        html: String,
    ): SendOutcome {
        val body =
            json.writeValueAsString(
                mapOf(
                    "chat_id" to chatId,
                    "text" to html,
                    "parse_mode" to "HTML",
                    "link_preview_options" to mapOf("is_disabled" to true),
                ),
            )
        return try {
            val response = http.send(request(body), HttpResponse.BodyHandlers.ofString())
            outcome(response.statusCode(), parse(response.body()))
        } catch (_: HttpTimeoutException) {
            SendOutcome.Transient("timeout")
        } catch (e: IOException) {
            SendOutcome.Transient(reason("network: ${e.javaClass.simpleName}", e.message))
        } catch (_: InterruptedException) {
            Thread.currentThread().interrupt()
            SendOutcome.Transient("interrupted")
        } catch (e: IllegalArgumentException) {
            // The JDK client names the full URI, token included, when it cannot use it.
            SendOutcome.Rejected(reason("invalid request: ${e.javaClass.simpleName}", e.message))
        }
    }

    /** Inside the caller's try: a URI the client cannot use fails here, with the token in the message. */
    private fun request(body: String): HttpRequest =
        HttpRequest
            .newBuilder(baseUri.resolve(token.path("sendMessage")))
            .timeout(requestTimeout)
            .header("Content-Type", "application/json")
            .POST(HttpRequest.BodyPublishers.ofString(body))
            .build()

    private fun outcome(
        status: Int,
        reply: Reply,
    ): SendOutcome {
        val reason = reason("HTTP $status", reply.description)
        return when {
            status == HTTP_OK && reply.ok -> SendOutcome.Delivered
            status == HTTP_TOO_MANY -> tooMany(reply, reason)
            status >= HTTP_SERVER_ERROR -> SendOutcome.Transient(reason)
            else -> SendOutcome.Rejected(reason)
        }
    }

    /** 429 with retry_after says how long to wait; without it, it is an ordinary transient failure. */
    private fun tooMany(
        reply: Reply,
        reason: String,
    ): SendOutcome = reply.retryAfter?.let { SendOutcome.RetryAfter(it) } ?: SendOutcome.Transient(reason)

    /** The fields of a Bot API reply that decide the outcome; a body that is not JSON has none. */
    private fun parse(body: String): Reply {
        val node =
            try {
                json.readTree(body)
            } catch (_: JacksonException) {
                return Reply.NONE
            }
        val description = node.path("description")
        val retryAfter = node.path("parameters").path("retry_after")
        return Reply(
            ok = node.path("ok").asBoolean(false),
            description = if (description.isString) description.asString() else null,
            retryAfter = if (retryAfter.isNumber) Duration.ofSeconds(retryAfter.asLong()) else null,
        )
    }

    private data class Reply(
        val ok: Boolean,
        val description: String?,
        val retryAfter: Duration?,
    ) {
        companion object {
            val NONE = Reply(ok = false, description = null, retryAfter = null)
        }
    }

    private fun reason(
        head: String,
        detail: String?,
    ): String {
        val text = if (detail.isNullOrBlank()) head else "$head: $detail"
        return token.scrub(text).take(MAX_REASON_CHARS)
    }
}
