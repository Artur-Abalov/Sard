// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.notify.telegram

import com.sun.net.httpserver.HttpExchange
import com.sun.net.httpserver.HttpServer
import java.net.InetSocketAddress
import java.net.URI
import java.util.concurrent.ConcurrentLinkedQueue
import java.util.concurrent.CopyOnWriteArrayList
import java.util.concurrent.CountDownLatch
import java.util.concurrent.Executors

/** A test bot token in Telegram's format; never a real one. */
internal const val TEST_TOKEN = "123456789:AAH-test_token_value_0123456789abcdef"

/** One request the fake received: the path holds the token, as Telegram's does. */
data class BotRequest(
    val method: String,
    val path: String,
    val contentType: String?,
    val body: String,
)

/** A scripted reply; [hold] keeps the exchange open until released, for timeout tests. */
data class BotReply(
    val status: Int,
    val body: String,
    val hold: CountDownLatch? = null,
)

/**
 * Telegram Bot API stand-in on the JDK's own HTTP server (S9a, answer В7: no new dependency).
 * Replies come from a script, then [fallback]; every request is recorded.
 */
class FakeBotApi : AutoCloseable {
    private val server = HttpServer.create(InetSocketAddress("127.0.0.1", 0), 0)
    private val script = ConcurrentLinkedQueue<BotReply>()
    val requests = CopyOnWriteArrayList<BotRequest>()

    @Volatile
    var fallback: BotReply = ok()

    init {
        server.executor = Executors.newCachedThreadPool { Thread(it, "fake-bot-api").apply { isDaemon = true } }
        server.createContext("/") { handle(it) }
        server.start()
    }

    val uri: URI get() = URI.create("http://127.0.0.1:${server.address.port}")

    fun reply(vararg replies: BotReply) {
        script.addAll(replies)
    }

    private fun handle(exchange: HttpExchange) {
        exchange.use {
            val body = it.requestBody.readAllBytes().decodeToString()
            val contentType = it.requestHeaders.getFirst("Content-Type")
            requests += BotRequest(it.requestMethod, it.requestURI.path, contentType, body)
            val reply = script.poll() ?: fallback
            reply.hold?.await()
            val bytes = reply.body.toByteArray()
            it.responseHeaders.add("Content-Type", "application/json")
            it.sendResponseHeaders(reply.status, bytes.size.toLong())
            it.responseBody.write(bytes)
        }
    }

    override fun close() {
        server.stop(0)
    }

    companion object {
        fun ok() = BotReply(200, """{"ok":true,"result":{"message_id":1}}""")

        fun tooMany(seconds: Int) =
            BotReply(
                429,
                """{"ok":false,"error_code":429,"description":"Too Many Requests: retry after $seconds",""" +
                    """"parameters":{"retry_after":$seconds}}""",
            )

        fun error(
            status: Int,
            description: String,
        ) = BotReply(status, """{"ok":false,"error_code":$status,"description":"$description"}""")
    }
}
