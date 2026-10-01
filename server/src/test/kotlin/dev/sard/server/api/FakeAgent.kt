// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import com.google.protobuf.Timestamp
import dev.sard.proto.agent.v1.BackupOutput
import dev.sard.proto.agent.v1.ConnectRequest
import dev.sard.proto.agent.v1.LogChunk
import dev.sard.proto.agent.v1.LogLevel
import dev.sard.proto.agent.v1.LogLine
import dev.sard.proto.agent.v1.StepPhase
import dev.sard.proto.agent.v1.StepProgress
import dev.sard.proto.agent.v1.StepResult
import dev.sard.proto.agent.v1.StepStatus
import dev.sard.server.agents.stream.Connection
import java.time.Duration
import java.time.Instant
import java.util.concurrent.TimeUnit

private val WAIT: Duration = Duration.ofSeconds(15)
private const val POLL_MILLIS = 25L

/** Repeats [assertion] until it passes, for the state that follows an asynchronous write of the server. */
fun <T> eventually(
    timeout: Duration = WAIT,
    assertion: () -> T,
): T {
    val deadline = System.nanoTime() + timeout.toNanos()
    while (true) {
        try {
            return assertion()
        } catch (e: AssertionError) {
            if (System.nanoTime() > deadline) throw e
            Thread.sleep(POLL_MILLIS)
        }
    }
}

/** The agent's side of a Connect stream: takes the steps the server sends and reports on them. */
class FakeAgent(
    private val connection: Connection,
) {
    /** The command id of the next RunStep the server sends. */
    fun nextStep(): String {
        while (true) {
            val message = checkNotNull(connection.received.poll(WAIT.toSeconds(), TimeUnit.SECONDS)) { "no RunStep within $WAIT" }
            if (message.hasRunStep()) return message.runStep.commandId
        }
    }

    fun progress(
        step: String,
        phase: StepPhase = StepPhase.STEP_PHASE_UPLOADING,
        bytes: Long = 0,
        total: Long = 0,
        files: Long = 0,
        filesTotal: Long = 0,
    ) = connection.send(
        ConnectRequest
            .newBuilder()
            .setStepProgress(
                StepProgress
                    .newBuilder()
                    .setCommandId(step)
                    .setPhase(phase)
                    .setBytesProcessed(bytes)
                    .setBytesTotal(total)
                    .setFilesProcessed(files)
                    .setFilesTotal(filesTotal),
            ).build(),
    )

    fun log(
        step: String,
        lines: List<LogLine>,
    ) = connection.send(
        ConnectRequest.newBuilder().setLogChunk(LogChunk.newBuilder().setCommandId(step).addAllLines(lines)).build(),
    )

    fun result(
        step: String,
        status: StepStatus,
        message: String = "",
        backup: BackupOutput? = null,
    ) = connection.send(
        ConnectRequest
            .newBuilder()
            .setStepResult(
                StepResult
                    .newBuilder()
                    .setCommandId(step)
                    .setStatus(status)
                    .setMessage(message)
                    .also { builder -> backup?.let(builder::setBackup) },
            ).build(),
    )

    companion object {
        fun line(
            text: String,
            level: LogLevel = LogLevel.LOG_LEVEL_INFO,
            time: Instant? = null,
        ): LogLine =
            LogLine
                .newBuilder()
                .setLevel(level)
                .setText(text)
                .also { builder -> time?.let { builder.setTime(Timestamp.newBuilder().setSeconds(it.epochSecond).setNanos(it.nano)) } }
                .build()

        fun backup(
            snapshot: String = "a1b2c3",
            total: Long = 1000,
            added: Long = 10,
            repository: String = "r0".padEnd(REPOSITORY_ID_LENGTH, '0'),
        ): BackupOutput =
            BackupOutput
                .newBuilder()
                .setSnapshotId(snapshot)
                .setTotalBytes(total)
                .setAddedBytes(added)
                .setRepositoryId(repository)
                .build()

        private const val REPOSITORY_ID_LENGTH = 64
    }
}
