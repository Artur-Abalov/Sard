// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.prometheus

import dev.sard.server.TestcontainersConfiguration
import dev.sard.server.api.captureLogs
import org.junit.jupiter.api.extension.ExtendWith
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.boot.test.system.CapturedOutput
import org.springframework.boot.test.system.OutputCaptureExtension
import org.springframework.boot.test.web.server.LocalServerPort
import org.springframework.context.annotation.Import
import tools.jackson.databind.ObjectMapper
import java.net.NetworkInterface
import java.net.URI
import java.net.http.HttpClient
import java.net.http.HttpRequest
import java.net.http.HttpResponse
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

/**
 * The server with the listener open to the network under a token and a reverse proxy in front of the console:
 * the token is required, it shows nowhere, and the proxy's headers open no way to the exposition.
 */
@ExtendWith(OutputCaptureExtension::class)
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.RANDOM_PORT,
    properties = [
        "spring.grpc.server.port=0",
        "sard.agent.stream.check-interval=1h",
        "sard.scheduler.interval=1h",
        "sard.freshness.interval=1h",
        "sard.verification.interval=1h",
        "sard.notify.tick-interval=1h",
        "sard.metrics.bind=0.0.0.0",
        "sard.metrics.token=$TOKEN",
        "server.forward-headers-strategy=native",
    ],
)
@Import(TestcontainersConfiguration::class)
class MetricsTokenIntegrationTest(
    @Autowired private val listener: MetricsListener,
    @Autowired private val mapper: ObjectMapper,
    @LocalServerPort private val httpPort: Int,
) {
    private val http = HttpClient.newHttpClient()

    private fun get(
        host: String,
        port: Int,
        path: String,
        headers: Map<String, String> = emptyMap(),
    ): HttpResponse<String> {
        val request = HttpRequest.newBuilder(URI.create("http://$host:$port$path"))
        headers.forEach { (name, value) -> request.header(name, value) }
        return http.send(request.build(), HttpResponse.BodyHandlers.ofString())
    }

    private fun bearer() = mapOf("Authorization" to "Bearer $TOKEN")

    /** An address of this machine that is not the loopback, where there is one; the loopback otherwise. */
    private fun outside(): String =
        NetworkInterface
            .networkInterfaces()
            .toList()
            .flatMap { it.inetAddresses.toList() }
            .firstOrNull { !it.isLoopbackAddress && it is java.net.Inet4Address }
            ?.hostAddress ?: "127.0.0.1"

    @Test
    fun `Слушатель наружу с токеном отдаёт экспозицию по верному токену с адреса вне loopback`() {
        val response = get(outside(), listener.boundPort, "/metrics", bearer())

        assertEquals(200, response.statusCode())
        assertTrue(
            response
                .headers()
                .firstValue("Content-Type")
                .get()
                .startsWith("text/plain; version=0.0.4"),
        )
        assertFalse(TOKEN in response.body())
    }

    @Test
    fun `Запрос без верного токена - 401 без тела и с WWW-Authenticate Bearer, на любой путь`() {
        for (path in listOf("/metrics", "/", "/actuator/health", "/actuator/prometheus", "/no-such")) {
            val response = get("127.0.0.1", listener.boundPort, path)
            assertEquals(401, response.statusCode(), path)
            assertEquals("", response.body(), path)
            assertEquals("Bearer", response.headers().firstValue("WWW-Authenticate").get(), path)
        }
    }

    @Test
    fun `Токен не попадает в ответы actuator основного порта, а эндпоинты с конфигурацией закрыты`() {
        val links = mapper.readTree(get("127.0.0.1", httpPort, "/actuator").body()).path("_links")
        assertEquals(setOf("self", "health", "health-path", "info"), links.propertyNames().toSet())
        val hrefs = links.propertyNames().map { links.path(it).path("href").asString() }.filter { '{' !in it }
        assertEquals(3, hrefs.size, hrefs.toString())
        for (href in hrefs) {
            val response = get("127.0.0.1", httpPort, URI.create(href).path)
            assertFalse(TOKEN in response.body(), href)
        }
        for (path in listOf("/actuator/env", "/actuator/configprops", "/actuator/prometheus")) {
            assertEquals(404, get("127.0.0.1", httpPort, path).statusCode(), path)
        }
    }

    @Test
    fun `Путь метрик за обратным прокси консоли не отдаёт экспозицию`() {
        val forwarded =
            bearer() +
                mapOf(
                    "X-Forwarded-Proto" to "https",
                    "X-Forwarded-For" to "203.0.113.7",
                    "X-Forwarded-Port" to "8081",
                    "X-Forwarded-Prefix" to "/metrics",
                )
        for (path in listOf("/actuator/prometheus", "/metrics", "//actuator/prometheus", "/;x/metrics")) {
            val response = get("127.0.0.1", httpPort, path, forwarded)
            assertTrue(response.body().lines().none { it.startsWith("sard_") || it.startsWith("# TYPE") }, path)
        }
    }

    @Test
    fun `Токен и неверный токен не попадают в журнал сервера при старте и запросах`(output: CapturedOutput) {
        val wrong = "wrong-token-0123456789abcdef0123456789"
        val lines =
            captureLogs {
                get("127.0.0.1", listener.boundPort, "/metrics", bearer())
                get("127.0.0.1", listener.boundPort, "/metrics", mapOf("Authorization" to "Bearer $wrong"))
            }

        assertFalse(lines.any { TOKEN in it || wrong in it })
        assertFalse(TOKEN in output.all)
        assertFalse(wrong in output.all)
    }
}
