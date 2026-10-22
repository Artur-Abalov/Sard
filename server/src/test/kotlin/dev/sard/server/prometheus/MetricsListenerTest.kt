// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.prometheus

import java.net.InetAddress
import java.net.ServerSocket
import java.net.URI
import java.net.http.HttpClient
import java.net.http.HttpRequest
import java.net.http.HttpResponse
import java.time.Duration
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit
import kotlin.test.AfterTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertFalse
import kotlin.test.assertTrue

private val LOOPBACK: InetAddress = InetAddress.ofLiteral("127.0.0.1")

/** The listener on a real socket: the glue between the endpoint and HTTP, its port and its life with the server. */
class MetricsListenerTest {
    private val started = mutableListOf<MetricsListener>()
    private val http = HttpClient.newHttpClient()

    @AfterTest
    fun `no listener outlives its test`() {
        started.forEach { it.stop() }
        started.clear()
    }

    private fun listener(
        token: String? = null,
        port: Int = 0,
        scrape: () -> String = { "sard_agents_connected 0.0\n" },
    ): MetricsListener {
        val settings = MetricsSettings(LOOPBACK, port, token?.let(::BearerToken), Duration.ofSeconds(60))
        return MetricsListener(settings, scrape).also {
            it.start()
            started += it
        }
    }

    private fun MetricsListener.get(
        path: String = "/metrics",
        method: String = "GET",
        authorization: String? = null,
    ): HttpResponse<String> {
        val request =
            HttpRequest
                .newBuilder(
                    URI.create("http://127.0.0.1:$boundPort$path"),
                ).method(method, HttpRequest.BodyPublishers.noBody())
        authorization?.let { request.header("Authorization", it) }
        return http.send(request.build(), HttpResponse.BodyHandlers.ofString())
    }

    @Test
    fun `Scrape на loopback без токена отдаёт экспозицию с Content-Type text-plain version 0-0-4`() {
        val response = listener().get()

        assertEquals(200, response.statusCode())
        assertEquals("text/plain; version=0.0.4; charset=utf-8", response.headers().firstValue("Content-Type").get())
        assertTrue(response.body().startsWith("sard_agents_connected"))
    }

    @Test
    fun `Ответ на неверный токен не отличается от ответа без токена, кроме Date`() {
        val guarded = listener(token = TOKEN)

        val absent = guarded.get()
        val wrong = guarded.get(authorization = "Bearer wrong-token")

        assertEquals(401, absent.statusCode())
        assertEquals("", absent.body())
        assertEquals("Bearer", absent.headers().firstValue("WWW-Authenticate").get())

        fun shape(response: HttpResponse<String>) = response.headers().map().filterKeys { !it.equals("date", true) }
        assertEquals(shape(absent), shape(wrong))
        assertEquals(absent.body(), wrong.body())
    }

    @Test
    fun `Верный токен, другой путь и другой метод по сети отвечают 200, 404 и 405`() {
        val guarded = listener(token = TOKEN)
        val bearer = "Bearer $TOKEN"

        assertEquals(200, guarded.get(authorization = bearer).statusCode())
        assertEquals(404, guarded.get("/actuator/health", authorization = bearer).statusCode())
        assertEquals(405, guarded.get(method = "POST", authorization = bearer).statusCode())
        assertEquals(401, guarded.get(method = "POST").statusCode())
    }

    @Test
    fun `Пятьдесят одновременных scrape отвечают все`() {
        val metrics = listener()
        val pool = Executors.newFixedThreadPool(50)

        val codes = List(50) { pool.submit<Int> { metrics.get().statusCode() } }.map { it.get(20, TimeUnit.SECONDS) }
        pool.shutdown()

        assertEquals(List(50) { 200 }, codes)
    }

    @Test
    fun `Слушатель закрывается вместе с остановкой и не принимает соединения`() {
        val metrics = listener()
        val port = metrics.boundPort
        assertTrue(metrics.isRunning)

        metrics.stop()

        assertFalse(metrics.isRunning)
        assertFailsWith<java.io.IOException> { java.net.Socket(LOOPBACK, port).close() }
    }

    @Test
    fun `Занятый порт не даёт слушателю стартовать и сообщение называет SARD_METRICS_PORT`() {
        ServerSocket(0, 1, LOOPBACK).use { busy ->
            val failure = assertFailsWith<IllegalStateException> { listener(port = busy.localPort) }

            assertTrue("SARD_METRICS_PORT" in failure.message.orEmpty(), failure.message)
        }
    }

    @Test
    fun `Сбой сбора экспозиции отвечает 500 без текста ошибки и слушатель продолжает отвечать`() {
        var calls = 0
        val metrics = listener { if (calls++ == 0) error("secret detail") else "sard_up 1.0\n" }

        val first = metrics.get()
        val second = metrics.get()

        assertEquals(500, first.statusCode())
        assertEquals("", first.body())
        assertEquals(200, second.statusCode())
    }
}
