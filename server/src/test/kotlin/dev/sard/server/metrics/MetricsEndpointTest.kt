// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.metrics

import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNull
import kotlin.test.assertTrue

private const val EXPOSITION = "# TYPE sard_agents_connected gauge\nsard_agents_connected 0.0\n"

/** What the metrics listener answers to a request: the token before the route, one path, one method (МР3). */
@MutFlowTest(includeTargets = [MetricsEndpoint::class, MetricsReply::class])
class MetricsEndpointTest {
    private val open = MetricsEndpoint(null) { EXPOSITION }
    private val guarded = MetricsEndpoint(BearerToken(TOKEN)) { EXPOSITION }

    private fun MetricsEndpoint.ask(
        method: String = "GET",
        path: String = "/metrics",
        authorization: String? = null,
    ) = MutFlow.underTest { reply(method, path, authorization) }

    @Test
    fun `Без токена на loopback GET metrics отдаёт экспозицию текстом формата 0-0-4`() {
        val reply = open.ask()

        assertEquals(200, reply.status)
        assertEquals("text/plain; version=0.0.4; charset=utf-8", reply.headers["Content-Type"])
        assertEquals(EXPOSITION, reply.body)
    }

    @Test
    fun `Верный токен открывает экспозицию`() {
        val reply = guarded.ask(authorization = "Bearer $TOKEN")

        assertEquals(200, reply.status)
        assertEquals(EXPOSITION, reply.body)
    }

    @Test
    fun `Запрос без верного токена отвечает 401 без тела и с WWW-Authenticate Bearer на любой путь и метод`() {
        val requests =
            listOf(
                "GET" to "/metrics",
                "GET" to "/",
                "GET" to "/actuator/health",
                "POST" to "/metrics",
                "GET" to "/no-such",
            )
        val headers = listOf(null, "", "Bearer", "Bearer wrong-token", "Basic Zm9v", "Token $TOKEN")
        for ((method, path) in requests) {
            for (header in headers) {
                val reply = guarded.ask(method, path, header)
                assertEquals(401, reply.status, "$method $path $header")
                assertNull(reply.body)
                assertEquals(mapOf("WWW-Authenticate" to "Bearer"), reply.headers)
            }
        }
    }

    @Test
    fun `Заданный токен без верного заголовка не открывает экспозицию и без токена на другом пути - 404`() {
        assertEquals(401, guarded.ask().status)
        assertEquals(404, open.ask(path = "/").status)
    }

    @Test
    fun `С верным токеном другой путь - 404 без тела`() {
        val paths =
            listOf(
                "/",
                "/index.html",
                "/api/v1/status",
                "/actuator",
                "/actuator/health",
                "/actuator/prometheus",
                "/v3/api-docs",
                "/downloads/agent/manifest.json",
                "/metrics/x",
                "/metrics/",
                "/Metrics",
            )
        for (path in paths) {
            val reply = guarded.ask(path = path, authorization = "Bearer $TOKEN")
            assertEquals(404, reply.status, path)
            assertNull(reply.body, path)
            assertTrue(reply.headers.isEmpty(), path)
        }
    }

    @Test
    fun `С верным токеном другой метод на metrics - 405 без тела`() {
        for (method in listOf("POST", "PUT", "DELETE", "HEAD", "PATCH", "OPTIONS", "get")) {
            val reply = guarded.ask(method, authorization = "Bearer $TOKEN")
            assertEquals(405, reply.status, method)
            assertNull(reply.body, method)
            assertEquals(mapOf("Allow" to "GET"), reply.headers, method)
        }
    }

    @Test
    fun `Сбой при сборе экспозиции - 500 без тела и без текста ошибки`() {
        val failing = MetricsEndpoint(null) { error("secret detail") }

        val reply = failing.ask()

        assertEquals(500, reply.status)
        assertNull(reply.body)
        assertTrue(reply.headers.isEmpty())
    }
}
