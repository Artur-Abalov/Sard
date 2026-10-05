// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.runs

import ch.qos.logback.classic.Logger
import ch.qos.logback.classic.spi.ILoggingEvent
import ch.qos.logback.core.read.ListAppender
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import org.slf4j.LoggerFactory
import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertEquals

@MutFlowTest
class RunFinishedTraceTest {
    private fun finished(
        n: Long,
        status: RunState,
    ) = RunFinished(UUID(1, 1), UUID(0, n), UUID(2, n), UUID(3, 3), Trigger.MANUAL, status, null, RUNS_NOW)

    private fun logged(block: () -> Unit): List<String> {
        val logger = LoggerFactory.getLogger(RunFinishedTrace::class.java) as Logger
        val appender = ListAppender<ILoggingEvent>().apply { start() }
        logger.addAppender(appender)
        try {
            block()
        } finally {
            logger.detachAppender(appender)
        }
        return appender.list.map { "${it.level} ${it.formattedMessage}" }
    }

    @Test
    fun `each published run leaves one info line and one count under its status`() {
        val counted = mutableListOf<RunState>()
        val trace = RunFinishedTrace { counted += it }

        val log =
            logged {
                MutFlow.underTest { trace.runFinished(finished(1, RunState.SUCCEEDED)) }
                MutFlow.underTest { trace.runFinished(finished(2, RunState.FAILED)) }
            }

        val expected = listOf("INFO run ${UUID(0, 1)} finished: succeeded", "INFO run ${UUID(0, 2)} finished: failed")
        assertEquals(expected, log)
        assertEquals(listOf(RunState.SUCCEEDED, RunState.FAILED), counted)
    }
}
