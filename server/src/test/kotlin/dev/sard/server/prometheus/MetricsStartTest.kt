// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.prometheus

import io.micrometer.core.instrument.Gauge
import io.micrometer.prometheusmetrics.PrometheusMeterRegistry
import org.junit.jupiter.api.extension.ExtendWith
import org.springframework.boot.test.context.ConfigDataApplicationContextInitializer
import org.springframework.boot.test.context.runner.ApplicationContextRunner
import org.springframework.boot.test.system.CapturedOutput
import org.springframework.boot.test.system.OutputCaptureExtension
import java.net.InetAddress
import java.net.InetSocketAddress
import java.net.ServerSocket
import java.net.Socket
import java.net.URI
import java.net.http.HttpClient
import java.net.http.HttpRequest
import java.net.http.HttpResponse
import java.time.Duration
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNotNull
import kotlin.test.assertTrue

private val LOOPBACK: InetAddress = InetAddress.ofLiteral("127.0.0.1")

private const val BIND = "sard.metrics.bind=0.0.0.0"
private const val CONTENT_TYPE = "text/plain; version=0.0.4; charset=utf-8"

/** The properties of a listener open to the network under the token. */
private fun outside(port: Int) = arrayOf(BIND, "sard.metrics.port=$port", "sard.metrics.token=$TOKEN")

private fun freePort(): Int = ServerSocket(0, 1, LOOPBACK).use { it.localPort }

private fun accepts(port: Int): Boolean =
    try {
        Socket().use { it.connect(InetSocketAddress(LOOPBACK, port), 1_000) }
        true
    } catch (_: java.io.IOException) {
        false
    }

/** The start of the metrics listener with `sard.metrics.*` as the operator sets them, without the database. */
@ExtendWith(OutputCaptureExtension::class)
class MetricsStartTest {
    private val runner = ApplicationContextRunner().withUserConfiguration(MetricsListenerConfiguration::class.java)

    private fun refusal(vararg properties: String): String {
        var message = ""
        runner.withPropertyValues(*properties).run { context ->
            val failure = assertNotNull(context.startupFailure, "the start must be refused")
            message = generateSequence<Throwable>(failure) { it.cause }.joinToString(" / ") { it.message.orEmpty() }
        }
        return message
    }

    private fun get(
        port: Int,
        path: String = "/metrics",
        authorization: String? = null,
    ): HttpResponse<String> {
        val request = HttpRequest.newBuilder(URI.create("http://127.0.0.1:$port$path"))
        authorization?.let { request.header("Authorization", it) }
        return HttpClient.newHttpClient().send(request.build(), HttpResponse.BodyHandlers.ofString())
    }

    @Test
    fun `Без SARD_METRICS_ в окружении слушатель на loopback, порт 8081, токена нет, тик раз в минуту`() {
        ApplicationContextRunner()
            .withInitializer(ConfigDataApplicationContextInitializer())
            .withUserConfiguration(MetricsSettingsConfiguration::class.java)
            .run { context ->
                val settings = context.getBean(MetricsSettings::class.java)
                assertEquals("127.0.0.1", settings.address.hostAddress)
                assertEquals(8081, settings.port)
                assertEquals(null, settings.token)
                assertEquals(Duration.ofSeconds(60), settings.stateInterval)
            }
    }

    @Test
    fun `Переменные SARD_METRICS_ окружения задают адрес, порт, токен и интервал`() {
        ApplicationContextRunner()
            .withInitializer(ConfigDataApplicationContextInitializer())
            .withUserConfiguration(MetricsSettingsConfiguration::class.java)
            .withSystemProperties(
                "SARD_METRICS_BIND=0.0.0.0",
                "SARD_METRICS_PORT=9100",
                "SARD_METRICS_TOKEN=$TOKEN",
                "SARD_METRICS_STATE_INTERVAL=10s",
            ).run { context ->
                val settings = context.getBean(MetricsSettings::class.java)
                assertEquals("0.0.0.0", settings.address.hostAddress)
                assertEquals(9100, settings.port)
                assertTrue(settings.token!!.accepts("Bearer $TOKEN"))
                assertEquals(Duration.ofSeconds(10), settings.stateInterval)
            }
    }

    @Test
    fun `Отказ старта из-за настроек не открывает порт и не печатает токен`(output: CapturedOutput) {
        val port = freePort()

        val short = TOKEN.take(31)
        val message = refusal("sard.metrics.bind=0.0.0.0", "sard.metrics.port=$port", "sard.metrics.token=$short")

        assertTrue("SARD_METRICS_TOKEN" in message, message)
        assertFalse(accepts(port))
        assertFalse(short in output.all)
        assertFalse(short in message)
    }

    @Test
    fun `Порт, совпавший с портом консоли и токен, не попадает в журнал при отказе`(output: CapturedOutput) {
        val message = refusal("sard.metrics.bind=0.0.0.0", "sard.metrics.port=8080", "sard.metrics.token=$TOKEN")

        assertTrue("SARD_METRICS_PORT" in message, message)
        assertFalse(TOKEN in output.all)
        assertFalse(TOKEN in message)
    }

    @Test
    fun `Занятый порт метрик не даёт серверу стартовать`() {
        ServerSocket(0, 1, LOOPBACK).use { busy ->
            val message = refusal("sard.metrics.port=${busy.localPort}")

            assertTrue("SARD_METRICS_PORT" in message, message)
        }
    }

    @Test
    fun `Слушатель по умолчанию отвечает на loopback без токена экспозицией`() {
        val port = freePort()

        runner.withPropertyValues("sard.metrics.port=$port").run { context ->
            val registry = context.getBean(PrometheusMeterRegistry::class.java)
            Gauge.builder("sard.agents.connected", registry) { 0.0 }.register(registry)

            val response = get(port)

            assertEquals(200, response.statusCode())
            assertEquals(CONTENT_TYPE, response.headers().firstValue("Content-Type").get())
            assertTrue(response.body().lines().any { it.startsWith("sard_agents_connected") }, response.body())
        }
    }

    @Test
    fun `Слушатель наружу отдаёт экспозицию по токену, токена нет в журнале и экспозиции`(output: CapturedOutput) {
        val port = freePort()

        runner.withPropertyValues(*outside(port)).run { context ->
            val registry = context.getBean(PrometheusMeterRegistry::class.java)
            Gauge.builder("sard.agents.connected", registry) { 0.0 }.register(registry)

            val allowed = get(port, authorization = "Bearer $TOKEN")
            val denied = get(port, authorization = "Bearer wrong-token-0123456789abcdef0123456789")

            assertEquals(200, allowed.statusCode())
            assertEquals(401, denied.statusCode())
            assertFalse(TOKEN in allowed.body())
            assertFalse(TOKEN in output.all, "the token is in the log")
            assertFalse("wrong-token-0123456789abcdef0123456789" in output.all, "the wrong token is in the log")
        }
    }

    @Test
    fun `Заданный токен требуется и на loopback`() {
        val port = freePort()

        runner.withPropertyValues("sard.metrics.port=$port", "sard.metrics.token=$TOKEN").run {
            assertEquals(401, get(port).statusCode())
            assertEquals(200, get(port, authorization = "Bearer $TOKEN").statusCode())
        }
    }

    @Test
    fun `Слушатель метрик закрывается вместе с сервером`() {
        val port = freePort()

        runner.withPropertyValues(*outside(port)).run {
            assertTrue(accepts(port))
        }

        assertFalse(accepts(port))
    }

    @Test
    fun `Адрес loopback без токена принимается`() {
        // ::1 is accepted by the settings (MetricsSettingsTest); a machine without IPv6 cannot open a socket on it.
        for (bind in listOf("127.0.0.1", "127.0.0.2")) {
            val port = freePort()
            runner.withPropertyValues("sard.metrics.bind=$bind", "sard.metrics.port=$port").run { context ->
                assertEquals(null, context.startupFailure, bind)
            }
        }
    }
}
