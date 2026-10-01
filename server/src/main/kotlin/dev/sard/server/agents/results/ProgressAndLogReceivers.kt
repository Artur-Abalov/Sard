// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents.results

import dev.sard.proto.agent.v1.LogChunk
import dev.sard.proto.agent.v1.StepProgress
import dev.sard.server.agents.stream.ConnectedAgent
import dev.sard.server.agents.stream.LogChunkHandler
import dev.sard.server.agents.stream.StepProgressHandler
import dev.sard.server.runs.Appended
import dev.sard.server.runs.LogLine
import dev.sard.server.runs.ProgressGate
import dev.sard.server.runs.ProgressReport
import dev.sard.server.runs.ProgressWritten
import org.slf4j.LoggerFactory
import java.util.UUID

private val progressLog = LoggerFactory.getLogger(StepProgressReceiver::class.java)
private val chunkLog = LoggerFactory.getLogger(LogChunkReceiver::class.java)
private val security = LoggerFactory.getLogger(SECURITY_LOG)
private val MOVED = setOf(ProgressWritten.STARTED, ProgressWritten.UPDATED)

/** StepProgressWrites::write. */
fun interface ProgressWriter {
    fun write(
        tenantId: UUID,
        agentId: UUID,
        stepId: UUID,
        report: ProgressReport,
    ): ProgressWritten
}

/** StepLogs::append; null when no step of this agent has this id. */
fun interface LogAppender {
    fun append(
        tenantId: UUID,
        agentId: UUID,
        stepId: UUID,
        lines: List<LogLine>,
    ): Appended?
}

interface LogMetrics {
    /** Lines not kept: past the step's limit, for no step of the agent, or not written. */
    fun dropped(lines: Int)

    /** A step's log reached its limit now. */
    fun truncated()
}

/** The step id of [commandId], or null after a line in the security log that never quotes it. */
private fun stepOf(
    agent: ConnectedAgent,
    commandId: String,
    what: String,
): UUID? {
    val stepId = runCatching { UUID.fromString(commandId) }.getOrNull()
    if (stepId == null) {
        security.warn(
            "agent {} (tenant {}) sent {} for a command id that is no UUID ({} characters)",
            agent.agentId,
            agent.tenantId,
            what,
            commandId.length,
        )
    }
    return stepId
}

/**
 * S5a's StepProgress handler (S7a). The [ProgressGate] (runs/ProgressThrottle) decides what
 * reaches the database; a failure is logged and the progress lost, never the stream: the next
 * message carries newer counters anyway (answer 3).
 */
class StepProgressReceiver(
    private val throttle: ProgressGate,
    private val writer: ProgressWriter,
) : StepProgressHandler {
    override fun handle(
        agent: ConnectedAgent,
        progress: StepProgress,
    ) {
        val stepId = stepOf(agent, progress.commandId, "progress") ?: return
        val report = ProtoReports.progress(progress)
        if (throttle.admit(agent.agentId, stepId, report.phase)) written(agent, stepId, write(agent, stepId, report))
    }

    /** Null when the write failed: logged, never the stream's end. */
    private fun write(
        agent: ConnectedAgent,
        stepId: UUID,
        report: ProgressReport,
    ): ProgressWritten? =
        runCatching { writer.write(agent.tenantId, agent.agentId, stepId, report) }
            .onFailure { progressLog.warn("progress of step {} not written: {}", stepId, it.javaClass.simpleName) }
            .getOrNull()

    /** A step that did not move lets its next message through at once; a foreign one is logged. */
    private fun written(
        agent: ConnectedAgent,
        stepId: UUID,
        written: ProgressWritten?,
    ) {
        if (written !in MOVED) throttle.forget(agent.agentId, stepId)
        if (written == ProgressWritten.UNKNOWN) {
            security.warn(
                "agent {} (tenant {}) sent progress for command {}, which is no step of it",
                agent.agentId,
                agent.tenantId,
                stepId,
            )
        }
    }
}

/**
 * S5a's LogChunk handler (S7a). Line text never reaches the server's log, not even inside an
 * exception message (PostgreSQL quotes failing rows): a failed append logs the exception's class.
 * A failure drops the chunk and keeps the stream: the agent does not send a chunk twice (answer 3).
 */
class LogChunkReceiver(
    private val appender: LogAppender,
    private val metrics: LogMetrics,
) : LogChunkHandler {
    override fun handle(
        agent: ConnectedAgent,
        chunk: LogChunk,
    ) {
        val count = chunk.linesCount
        val stepId = stepOf(agent, chunk.commandId, "$count log lines")
        val appended =
            stepId?.let { step ->
                runCatching { appender.append(agent.tenantId, agent.agentId, step, ProtoReports.lines(chunk)) }
                    .onFailure { dropped(step, count, it) }
                    .getOrElse { return metrics.dropped(count) }
            }
        if (stepId != null && appended == null) {
            security.warn(
                "agent {} (tenant {}) sent {} log lines for command {}, which is no step of it",
                agent.agentId,
                agent.tenantId,
                count,
                stepId,
            )
        }
        record(appended, count)
    }

    private fun dropped(
        stepId: UUID,
        count: Int,
        failure: Throwable,
    ) = chunkLog.warn("log of step {}: {} lines dropped, not written: {}", stepId, count, failure.javaClass.simpleName)

    private fun record(
        appended: Appended?,
        count: Int,
    ) {
        val dropped = appended?.dropped ?: count
        if (dropped > 0) metrics.dropped(dropped)
        if (appended?.marked == true) metrics.truncated()
    }
}
