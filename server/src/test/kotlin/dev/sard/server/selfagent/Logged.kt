// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.selfagent

import ch.qos.logback.classic.Level
import ch.qos.logback.classic.Logger
import ch.qos.logback.classic.spi.ILoggingEvent
import ch.qos.logback.core.read.ListAppender
import org.slf4j.LoggerFactory

/** One captured log line: its [level] and the text with the lines of every cause. */
data class Logged(
    val level: Level,
    val text: String,
)

/** Everything the server logs, at DEBUG, while [block] runs. */
fun captureEvents(block: () -> Unit): List<Logged> {
    val root = LoggerFactory.getLogger(org.slf4j.Logger.ROOT_LOGGER_NAME) as Logger
    val appender = ListAppender<ILoggingEvent>()
    appender.start()
    val level = root.level
    root.level = Level.DEBUG
    root.addAppender(appender)
    try {
        block()
    } finally {
        root.detachAppender(appender)
        appender.stop()
        root.level = level
    }
    return appender.list.map { event ->
        val causes = generateSequence(event.throwableProxy) { it.cause }.mapNotNull { it.message }
        Logged(event.level, (listOf(event.formattedMessage) + causes).joinToString("\n"))
    }
}
