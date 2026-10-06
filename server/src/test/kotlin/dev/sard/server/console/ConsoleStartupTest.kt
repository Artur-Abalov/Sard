// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.console

import ch.qos.logback.classic.Level
import ch.qos.logback.classic.Logger
import ch.qos.logback.classic.spi.ILoggingEvent
import ch.qos.logback.core.read.ListAppender
import org.slf4j.LoggerFactory
import org.springframework.boot.autoconfigure.AutoConfigurations
import org.springframework.boot.test.context.runner.ApplicationContextRunner
import kotlin.test.AfterTest
import kotlin.test.BeforeTest
import kotlin.test.Test
import kotlin.test.assertEquals

private const val WITH_CONSOLE = "classpath:/fixtures/console/"
private const val WITHOUT_CONSOLE = "classpath:/fixtures/"

/** Rule "Сервер без консоли отвечает 404 на пути консоли и работает как прежде" (@startup). */
class ConsoleStartupTest {
    private val runner =
        ApplicationContextRunner().withConfiguration(AutoConfigurations.of(ConsoleAutoConfiguration::class.java))
    private val appender = ListAppender<ILoggingEvent>()
    private val rootLogger = LoggerFactory.getLogger(Logger.ROOT_LOGGER_NAME) as Logger

    @BeforeTest
    fun `capture the logs`() {
        appender.start()
        rootLogger.addAppender(appender)
    }

    @AfterTest
    fun `stop capturing the logs`() {
        rootLogger.detachAppender(appender)
    }

    private fun startWith(location: String): List<ILoggingEvent> {
        runner.withPropertyValues("sard.console.location=$location").run { }
        return appender.list.filter { "console" in it.formattedMessage.lowercase() }
    }

    @Test
    fun `a server without the console logs one INFO record about it at startup`() {
        val records = startWith(WITHOUT_CONSOLE)
        assertEquals(1, records.size, records.joinToString { it.formattedMessage })
        assertEquals(Level.INFO, records.single().level)
        assertEquals(true, "not included in this build" in records.single().formattedMessage)
    }

    @Test
    fun `a server with the console does not log that it is missing`() {
        assertEquals(emptyList(), startWith(WITH_CONSOLE))
    }
}
