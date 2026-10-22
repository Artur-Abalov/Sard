// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.prometheus

import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import org.springframework.boot.test.context.runner.ApplicationContextRunner
import java.time.Duration
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertFalse
import kotlin.test.assertNotNull
import kotlin.test.assertNull
import kotlin.test.assertTrue

const val TOKEN = "m3tr1cs-T0ken-0123456789abcdef0123456789"

/** `sard.metrics.*` as the operator sets it with SARD_METRICS_*: what stops the start, and what is accepted. */
@MutFlowTest(includeTargets = [MetricsProperties::class, BearerToken::class])
class MetricsSettingsTest {
    private fun settings(
        bind: String = "127.0.0.1",
        port: Int = 8081,
        token: String = "",
        interval: Duration = Duration.ofSeconds(60),
    ) = MutFlow.underTest { MetricsProperties(bind, port, token, interval).settings() }

    private fun refusal(
        bind: String = "127.0.0.1",
        port: Int = 8081,
        token: String = "",
        interval: Duration = Duration.ofSeconds(60),
    ): String = assertFailsWith<IllegalArgumentException> { settings(bind, port, token, interval) }.message.orEmpty()

    @Test
    fun `По умолчанию слушатель на loopback и порту 8081 без токена с тиком раз в минуту`() {
        val settings = settings()

        assertEquals("127.0.0.1", settings.address.hostAddress)
        assertEquals(8081, settings.port)
        assertNull(settings.token)
        assertEquals(Duration.ofSeconds(60), settings.stateInterval)
    }

    @Test
    fun `Адрес вне loopback без токена не даёт серверу стартовать и сообщение называет обе переменные`() {
        for (bind in listOf("0.0.0.0", "::", "172.18.0.5")) {
            val message = refusal(bind = bind)
            assertTrue("SARD_METRICS_TOKEN" in message && "SARD_METRICS_BIND" in message, "$bind: $message")
        }
    }

    @Test
    fun `Пустой токен не считается заданным`() {
        val message = refusal(bind = "0.0.0.0", token = "   ")

        assertTrue("SARD_METRICS_TOKEN" in message, message)
    }

    @Test
    fun `Токен короче 32 символов не даёт серверу стартовать и сообщение не содержит его значения`() {
        val short = TOKEN.take(31)
        for (bind in listOf("0.0.0.0", "127.0.0.1")) {
            val message = refusal(bind = bind, token = short)
            assertTrue("SARD_METRICS_TOKEN" in message, message)
            assertFalse(short in message, message)
        }
        assertNotNull(settings(bind = "0.0.0.0", token = TOKEN.take(32)).token)
    }

    @Test
    fun `Адрес loopback без токена принимается`() {
        for (bind in listOf("127.0.0.1", "127.0.0.2", "::1")) {
            assertTrue(settings(bind = bind).address.isLoopbackAddress, bind)
        }
    }

    @Test
    fun `Заданный токен принимается на любом адресе`() {
        assertEquals("0.0.0.0", settings(bind = "0.0.0.0", token = TOKEN).address.hostAddress)
    }

    @Test
    fun `Имя хоста и неверный IP-адрес не принимаются, адрес только IP-литерал`() {
        for (bind in listOf("metrics.example.com", "999.1.1.1", "localhost", "")) {
            assertTrue("SARD_METRICS_BIND" in refusal(bind = bind), bind)
        }
    }

    @Test
    fun `Порт вне диапазона и совпадающий с основным или gRPC портом не принимается`() {
        for (port in listOf(0, -1, 65536, 8080, 9090)) {
            assertTrue("SARD_METRICS_PORT" in refusal(port = port), "$port")
        }
        assertEquals(1, settings(port = 1).port)
        assertEquals(65535, settings(port = 65535).port)
    }

    @Test
    fun `Неположительный интервал тика не принимается`() {
        for (interval in listOf(Duration.ZERO, Duration.ofSeconds(-1))) {
            assertTrue("sard.metrics.state-interval" in refusal(interval = interval), "$interval")
        }
        assertEquals(Duration.ofMillis(1), settings(interval = Duration.ofMillis(1)).stateInterval)
    }

    @Test
    fun `Значение токена не попадает в текстовое представление настроек`() {
        val settings = settings(bind = "0.0.0.0", token = TOKEN)

        assertFalse(TOKEN in settings.toString(), settings.toString())
        assertFalse(TOKEN in MetricsProperties("0.0.0.0", 8081, TOKEN, Duration.ofSeconds(60)).toString())
    }

    @Test
    fun `Токен сравнивается целиком с заголовком Bearer и ни с чем другим`() {
        val token = assertNotNull(settings(bind = "0.0.0.0", token = TOKEN).token)

        assertTrue(token.accepts("Bearer $TOKEN"))
        val refused =
            listOf(
                null,
                "",
                "Bearer",
                "Bearer ",
                "Bearer wrong-token",
                "Bearer ${TOKEN.dropLast(1)}",
                "Bearer ${TOKEN}0",
                "Bearer ${TOKEN.uppercase()}",
                "Token $TOKEN",
                "bearer $TOKEN",
                TOKEN,
            )
        for (header in refused) assertFalse(token.accepts(header), "$header")
    }

    @Test
    fun `Свойства sard-metrics читаются из окружения сервера, неверное значение останавливает старт`() {
        val runner = ApplicationContextRunner().withUserConfiguration(MetricsSettingsConfiguration::class.java)

        runner
            .withPropertyValues(
                "sard.metrics.bind=127.0.0.2",
                "sard.metrics.port=9100",
                "sard.metrics.state-interval=10s",
            ).run { context ->
                val bound = context.getBean(MetricsSettings::class.java)
                assertEquals(
                    listOf("127.0.0.2", 9100, Duration.ofSeconds(10)),
                    listOf(bound.address.hostAddress, bound.port, bound.stateInterval),
                )
            }
        runner.withPropertyValues("sard.metrics.bind=0.0.0.0").run { context ->
            val failure = assertNotNull(context.startupFailure)
            val chain = generateSequence<Throwable>(failure) { it.cause }.joinToString(" / ") { it.message.orEmpty() }
            assertTrue("SARD_METRICS_TOKEN" in chain && "SARD_METRICS_BIND" in chain, chain)
        }
    }
}
