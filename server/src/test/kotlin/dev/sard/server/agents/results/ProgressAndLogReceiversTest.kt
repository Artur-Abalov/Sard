// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents.results

import ch.qos.logback.classic.Level
import ch.qos.logback.classic.Logger
import ch.qos.logback.classic.spi.ILoggingEvent
import ch.qos.logback.core.read.ListAppender
import com.google.protobuf.Timestamp
import dev.sard.proto.agent.v1.LogChunk
import dev.sard.proto.agent.v1.LogLevel
import dev.sard.proto.agent.v1.StepPhase
import dev.sard.proto.agent.v1.StepProgress
import dev.sard.server.agents.stream.ConnectedAgent
import dev.sard.server.runs.Appended
import dev.sard.server.runs.LogLine
import dev.sard.server.runs.ProgressGate
import dev.sard.server.runs.ProgressReport
import dev.sard.server.runs.ProgressWritten
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import org.slf4j.LoggerFactory
import java.time.Instant
import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

private val AGENT = ConnectedAgent(UUID.randomUUID(), UUID.randomUUID(), "01")
private val STEP: UUID = UUID.randomUUID()
private val NOW: Instant = Instant.parse("2026-09-30T10:00:00Z")
private const val SECRET_TEXT = "password=hunter2"
private val FROM = "agent ${AGENT.agentId} (tenant ${AGENT.tenantId})"

private class Writes(
    var answer: () -> ProgressWritten = { ProgressWritten.UPDATED },
) : ProgressWriter {
    val reports = mutableListOf<ProgressReport>()

    override fun write(
        tenantId: UUID,
        agentId: UUID,
        stepId: UUID,
        report: ProgressReport,
    ): ProgressWritten {
        reports += report
        return answer()
    }
}

private class Appends(
    var answer: () -> Appended? = { Appended(1, 0, false) },
) : LogAppender {
    val calls = mutableListOf<Triple<UUID, UUID, List<LogLine>>>()

    override fun append(
        tenantId: UUID,
        agentId: UUID,
        stepId: UUID,
        lines: List<LogLine>,
    ): Appended? {
        calls += Triple(tenantId, agentId, lines)
        return answer()
    }
}

/** Admits what [admitted] says and records what it was asked. */
private class FakeGate(
    var admitted: Boolean = true,
) : ProgressGate {
    val asked = mutableListOf<String?>()
    val forgotten = mutableListOf<Pair<UUID, UUID>>()

    override fun admit(
        agentId: UUID,
        stepId: UUID,
        phase: String?,
    ): Boolean {
        asked += phase
        return admitted
    }

    override fun forget(
        agentId: UUID,
        stepId: UUID,
    ) {
        forgotten += agentId to stepId
    }
}

private class RecordingLogMetrics : LogMetrics {
    val droppedCalls = mutableListOf<Int>()
    val dropped get() = droppedCalls.sum()
    var truncated = 0

    override fun dropped(lines: Int) {
        droppedCalls += lines
    }

    override fun truncated() {
        truncated++
    }
}

/** Progress and log handlers of S5a (S7a): throttling, ownership, failures that never end the stream. */
@MutFlowTest
class ProgressAndLogReceiversTest {
    private val gate = FakeGate()
    private val writes = Writes()
    private val progress = StepProgressReceiver(gate, writes)
    private val appends = Appends()
    private val metrics = RecordingLogMetrics()
    private val logs = LogChunkReceiver(appends, metrics)

    private fun progress(
        phase: StepPhase = StepPhase.STEP_PHASE_UPLOADING,
        processed: Long = 10,
        total: Long = 100,
        command: String = STEP.toString(),
    ) = StepProgress
        .newBuilder()
        .setCommandId(command)
        .setPhase(phase)
        .setBytesProcessed(processed)
        .setBytesTotal(total)
        .build()

    private fun chunk(
        vararg lines: Pair<LogLevel, String>,
        command: String = STEP.toString(),
    ) = LogChunk
        .newBuilder()
        .setCommandId(command)
        .addAllLines(
            lines.map { (level, text) ->
                dev.sard.proto.agent.v1.LogLine
                    .newBuilder()
                    .setLevel(level)
                    .setText(text)
                    .setTime(Timestamp.newBuilder().setSeconds(NOW.epochSecond))
                    .build()
            },
        ).build()

    private fun everyLog(block: () -> Unit): List<ILoggingEvent> {
        val root = LoggerFactory.getLogger(Logger.ROOT_LOGGER_NAME) as Logger
        val level = root.level
        val appender = ListAppender<ILoggingEvent>()
        appender.start()
        root.addAppender(appender)
        root.level = Level.DEBUG
        try {
            block()
        } finally {
            root.level = level
            root.detachAppender(appender)
            appender.stop()
        }
        return appender.list
    }

    // --- progress

    @Test
    fun `progress is written with its phase and counters`() {
        MutFlow.underTest { progress.handle(AGENT, progress()) }

        assertEquals(listOf(ProgressReport("uploading", 10, 100)), writes.reports)
    }

    @Test
    fun `every phase is written by its name, an unspecified one as unknown`() {
        val phases =
            mapOf(
                StepPhase.STEP_PHASE_ACCEPTED to "accepted",
                StepPhase.STEP_PHASE_PREPARING to "preparing",
                StepPhase.STEP_PHASE_DUMPING to "dumping",
                StepPhase.STEP_PHASE_UPLOADING to "uploading",
                StepPhase.STEP_PHASE_RESTORING to "restoring",
                StepPhase.STEP_PHASE_VERIFYING to "verifying",
                StepPhase.STEP_PHASE_UNSPECIFIED to null,
            )
        for ((phase, stored) in phases) {
            val report = MutFlow.underTest { ProtoReports.progress(progress(phase)) }
            assertEquals(stored, report.phase)
        }
    }

    @Test
    fun `a total of zero is unknown, and counters beyond the signed range are dropped`() {
        val report = MutFlow.underTest { ProtoReports.progress(progress(processed = -1, total = 0)) }

        assertEquals(ProgressReport("uploading", null, null), report)
    }

    @Test
    fun `progress the gate holds back is not written`() {
        gate.admitted = false

        MutFlow.underTest { progress.handle(AGENT, progress()) }

        assertEquals(listOf<String?>("uploading"), gate.asked)
        assertEquals(emptyList(), writes.reports)
        assertEquals(emptyList(), gate.forgotten)
    }

    @Test
    fun `a step that moved stays in the gate, one that did not is forgotten`() {
        val forgets =
            mapOf(
                ProgressWritten.STARTED to false,
                ProgressWritten.UPDATED to false,
                ProgressWritten.CLOSED to true,
                ProgressWritten.UNKNOWN to true,
            )
        for ((answer, forgotten) in forgets) {
            writes.answer = { answer }
            gate.forgotten.clear()

            MutFlow.underTest { progress.handle(AGENT, progress()) }

            assertEquals(if (forgotten) listOf(AGENT.agentId to STEP) else emptyList(), gate.forgotten)
        }
    }

    @Test
    fun `a counter of zero is known, only a negative one is not`() {
        val report = MutFlow.underTest { ProtoReports.progress(progress(processed = 0, total = 1)) }

        assertEquals(ProgressReport("uploading", 0, 1), report)
    }

    @Test
    fun `a phase the server does not know is unknown`() {
        val unknown =
            StepProgress
                .newBuilder()
                .setCommandId(STEP.toString())
                .setPhaseValue(99)
                .build()

        assertEquals(null, MutFlow.underTest { ProtoReports.progress(unknown) }.phase)
    }

    @Test
    fun `progress for no step of this agent goes to the security log`() {
        writes.answer = { ProgressWritten.UNKNOWN }

        val events = everyLog { MutFlow.underTest { progress.handle(AGENT, progress()) } }

        val security = events.single { it.loggerName == SECURITY_LOG }
        assertEquals(Level.WARN, security.level)
        assertEquals(
            "$FROM sent progress for command $STEP, which is no step of it",
            security.formattedMessage,
        )
    }

    @Test
    fun `progress with a command id that is no UUID is dropped and logged without its text`() {
        val events = everyLog { MutFlow.underTest { progress.handle(AGENT, progress(command = "x\ny")) } }

        assertEquals(emptyList(), writes.reports)
        val expected = "$FROM sent progress for a command id that is no UUID (3 characters)"
        assertEquals(listOf(expected), events.filter { it.loggerName == SECURITY_LOG }.map { it.formattedMessage })
    }

    @Test
    fun `a failed progress write is logged and does not end the stream`() {
        writes.answer = { throw IllegalStateException("database down") }

        val events = everyLog { MutFlow.underTest { progress.handle(AGENT, progress()) } }
        writes.answer = { ProgressWritten.UPDATED }
        MutFlow.underTest { progress.handle(AGENT, progress()) }

        assertEquals(
            listOf("progress of step $STEP not written: IllegalStateException"),
            events
                .filter {
                    it.level == Level.WARN
                }.map { it.formattedMessage },
        )
        assertEquals(2, writes.reports.size)
        assertEquals(listOf(AGENT.agentId to STEP), gate.forgotten)
    }

    // --- logs

    @Test
    fun `log lines keep their order, level, time and text`() {
        val levels =
            listOf(
                LogLevel.LOG_LEVEL_DEBUG,
                LogLevel.LOG_LEVEL_INFO,
                LogLevel.LOG_LEVEL_WARN,
                LogLevel.LOG_LEVEL_ERROR,
                LogLevel.LOG_LEVEL_UNSPECIFIED,
            )

        MutFlow.underTest { logs.handle(AGENT, chunk(*levels.map { it to it.name }.toTypedArray())) }

        val (tenant, agent, lines) = appends.calls.single()
        assertEquals(listOf(AGENT.tenantId, AGENT.agentId), listOf(tenant, agent))
        val expected =
            listOf("debug", "info", "warn", "error", "info").zip(levels).map { (level, proto) ->
                LogLine(NOW, level, proto.name)
            }
        assertEquals(expected, lines)
    }

    @Test
    fun `a line without a time keeps none`() {
        val line =
            dev.sard.proto.agent.v1.LogLine
                .newBuilder()
                .setLevel(LogLevel.LOG_LEVEL_INFO)
                .setText("t")
        val chunk =
            LogChunk
                .newBuilder()
                .setCommandId(STEP.toString())
                .addLines(line)
                .build()

        MutFlow.underTest { logs.handle(AGENT, chunk) }

        assertEquals(listOf(LogLine(null, "info", "t")), appends.calls.single().third)
    }

    @Test
    fun `truncation and dropped lines are counted`() {
        appends.answer = { Appended(kept = 1, dropped = 3, marked = true) }

        MutFlow.underTest { logs.handle(AGENT, chunk(LogLevel.LOG_LEVEL_INFO to "a")) }

        assertEquals(listOf(3, 1), listOf(metrics.dropped, metrics.truncated))
    }

    @Test
    fun `lines within the limits count nothing`() {
        MutFlow.underTest { logs.handle(AGENT, chunk(LogLevel.LOG_LEVEL_INFO to "a")) }

        assertEquals(emptyList(), metrics.droppedCalls)
        assertEquals(0, metrics.truncated)
    }

    @Test
    fun `lines for no step of this agent are dropped, counted and logged for security`() {
        appends.answer = { null }

        val events =
            everyLog {
                val two = chunk(LogLevel.LOG_LEVEL_INFO to "a", LogLevel.LOG_LEVEL_INFO to "b")
                MutFlow.underTest { logs.handle(AGENT, two) }
            }

        assertEquals(2, metrics.dropped)
        val expected = "$FROM sent 2 log lines for command $STEP, which is no step of it"
        assertEquals(listOf(expected), events.filter { it.loggerName == SECURITY_LOG }.map { it.formattedMessage })
    }

    @Test
    fun `lines with a command id that is no UUID are dropped unread`() {
        val nope = chunk(LogLevel.LOG_LEVEL_INFO to "a", command = "nope")
        val events = everyLog { MutFlow.underTest { logs.handle(AGENT, nope) } }

        assertEquals(emptyList(), appends.calls)
        assertEquals(1, metrics.dropped)
        val expected = "$FROM sent 1 log lines for a command id that is no UUID (4 characters)"
        assertEquals(listOf(expected), events.filter { it.loggerName == SECURITY_LOG }.map { it.formattedMessage })
    }

    @Test
    fun `a failed append drops the chunk, counts it and logs no line text`() {
        appends.answer = { throw IllegalStateException("Failing row contains ($SECRET_TEXT)") }

        val secret = chunk(LogLevel.LOG_LEVEL_INFO to SECRET_TEXT)
        val events = everyLog { MutFlow.underTest { logs.handle(AGENT, secret) } }

        assertEquals(1, metrics.dropped)
        assertEquals(
            listOf("log of step $STEP: 1 lines dropped, not written: IllegalStateException"),
            events
                .filter {
                    it.level ==
                        Level.WARN
                }.map { it.formattedMessage },
        )
        assertTrue(events.none { SECRET_TEXT in it.formattedMessage || it.throwableProxy != null })
    }

    @Test
    fun `no log line text reaches the server log`() {
        val events =
            everyLog {
                MutFlow.underTest { logs.handle(AGENT, chunk(LogLevel.LOG_LEVEL_ERROR to SECRET_TEXT)) }
                appends.answer = { Appended(0, 1, true) }
                MutFlow.underTest { logs.handle(AGENT, chunk(LogLevel.LOG_LEVEL_ERROR to SECRET_TEXT)) }
            }

        assertTrue(events.none { SECRET_TEXT in it.formattedMessage })
    }
}
