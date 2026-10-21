// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.metrics

import ch.qos.logback.classic.Level
import ch.qos.logback.classic.Logger
import ch.qos.logback.classic.spi.ILoggingEvent
import ch.qos.logback.core.read.ListAppender
import dev.sard.server.pki.MovableClock
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import io.micrometer.prometheusmetrics.PrometheusConfig
import io.micrometer.prometheusmetrics.PrometheusMeterRegistry
import org.slf4j.LoggerFactory
import java.sql.SQLException
import java.time.Instant
import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNull
import kotlin.test.assertTrue

private val AT: Instant = Instant.parse("2027-06-15T12:00:00Z")
private val SOURCE: UUID = UUID.fromString("00000000-0000-7000-8000-00000000000a")

private const val BACKUP = "sard_source_last_backup_success_timestamp_seconds"

private fun Exposition.backupOf(source: UUID) = value(BACKUP, "source_id" to "$source")

private fun snapshotAt(backup: Instant) =
    StateSnapshot(
        listOf(SourceSuccess(SOURCE, backup, null)),
        emptyMap(),
        emptyMap(),
        emptyMap(),
        emptyMap(),
        emptyMap(),
        null,
        AT,
        "ok",
        null,
    )

/** A tick: it reads the state and puts it in the gauges; a failing one changes nothing but its own mark. */
@MutFlowTest(includeTargets = [StateRefresh::class])
class StateRefreshTest {
    private val registry = PrometheusMeterRegistry(PrometheusConfig.DEFAULT)
    private val clock = MovableClock(AT)
    private var backup: Instant = AT.minusSeconds(86_400)
    private var failure: Exception? = null
    private val refresh =
        StateRefresh(
            { failure?.let { throw it } ?: snapshotAt(backup) },
            StateGauges(registry),
            clock,
            registry,
        )

    private fun tick() = MutFlow.underTest { refresh.tick() }

    private fun exposition() = Exposition(registry.scrape())

    private fun warnings(block: () -> Unit): List<String> {
        val logger = LoggerFactory.getLogger(StateRefresh::class.java) as Logger
        val appender = ListAppender<ILoggingEvent>().apply { start() }
        logger.addAppender(appender)
        try {
            block()
        } finally {
            logger.detachAppender(appender)
        }
        return appender.list.filter { it.level == Level.WARN }.map { it.formattedMessage }
    }

    @Test
    fun `Тик кладёт состояние в метрики и время тика`() {
        tick()

        val now = exposition()
        assertEquals(AT.epochSecond - 86_400.0, now.backupOf(SOURCE))
        assertEquals(AT.epochSecond.toDouble(), now.value("sard_metrics_state_refresh_timestamp_seconds"))
        assertEquals(0.0, now.value("sard_metrics_state_refresh_failures_total"))
    }

    @Test
    fun `Тик, упавший на недоступной базе, оставляет значения и время прошлого тика и растёт счётчик сбоев`() {
        tick()
        failure = SQLException("select secret from sources where id = '$SOURCE'")
        clock.now = AT.plusSeconds(60)

        tick()

        val now = exposition()
        assertEquals(AT.epochSecond - 86_400.0, now.backupOf(SOURCE))
        assertEquals(AT.epochSecond.toDouble(), now.value("sard_metrics_state_refresh_timestamp_seconds"))
        assertEquals(1.0, now.value("sard_metrics_state_refresh_failures_total"))
    }

    @Test
    fun `Сбой пишет одну строку WARN с классом исключения без текста SQL и идентификаторов`() {
        failure = SQLException("select secret from sources where id = '$SOURCE'")

        val lines = warnings { tick() }

        assertEquals(1, lines.size, lines.toString())
        assertTrue("java.sql.SQLException" in lines.single(), lines.single())
        assertFalse("select" in lines.single() || "$SOURCE" in lines.single(), lines.single())
    }

    @Test
    fun `До первого успешного тика после сбоя метрик состояния нет, счётчик сбоев есть`() {
        failure = IllegalStateException("database is down")

        tick()

        val now = exposition()
        assertNull(now.value("sard_metrics_state_refresh_timestamp_seconds"))
        assertFalse(now.has("sard_source_last_backup_success_timestamp_seconds"))
        assertEquals(1.0, now.value("sard_metrics_state_refresh_failures_total"))
    }

    @Test
    fun `После возврата базы следующий тик обновляет значения и время`() {
        tick()
        failure = IllegalStateException("database is down")
        clock.now = AT.plusSeconds(60)
        tick()
        failure = null
        backup = AT.plusSeconds(30)
        clock.now = AT.plusSeconds(120)

        tick()

        val now = exposition()
        assertEquals(AT.epochSecond + 30.0, now.backupOf(SOURCE))
        assertEquals(AT.epochSecond + 120.0, now.value("sard_metrics_state_refresh_timestamp_seconds"))
        assertEquals(1.0, now.value("sard_metrics_state_refresh_failures_total"))
    }
}
