// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.runs

import java.time.Instant

private const val CUT = "…[truncated]"
private const val NUL = "\u0000"

/** One line of a step's log; [time] is the agent's clock, null for a line the server wrote. */
data class LogLine(
    val time: Instant?,
    /** debug, info, warn or error. */
    val level: String,
    val text: String,
) {
    /** Never the text: log lines may carry what the agent failed to redact. */
    override fun toString() = "LogLine(time=$time, level=$level, ${text.length} characters)"
}

/** `sard.logs.*`: bytes of one line's text, and of all the text one step keeps. */
data class LogLimits(
    val maxLineBytes: Int,
    val maxStepBytes: Long,
)

/** The lines of a chunk that are kept, their UTF-8 bytes, and whether the step's log is cut now. */
data class LogFit(
    val kept: List<LogLine>,
    val bytes: Long,
    val mark: Boolean,
)

/**
 * The limits of S7a (answer 7). A line longer than [LogLimits.maxLineBytes] is cut at a character
 * boundary and marked; the first line past [LogLimits.maxStepBytes] ends the step's log with one
 * mark ([mark]), and every later line is dropped. NUL is removed: PostgreSQL text cannot hold it.
 */
object LogBudget {
    fun fit(
        lines: List<LogLine>,
        used: Long,
        truncated: Boolean,
        limits: LogLimits,
    ): LogFit {
        val kept = mutableListOf<LogLine>()
        var bytes = 0L
        for (line in lines.takeIf { !truncated }.orEmpty()) {
            val (text, size) = cut(line.text.replace(NUL, ""), limits.maxLineBytes)
            if (used + bytes + size > limits.maxStepBytes) return LogFit(kept, bytes, mark = true)
            kept += line.copy(text = text)
            bytes += size
        }
        return LogFit(kept, bytes, mark = false)
    }

    /** The server's line that ends a truncated log. */
    fun mark(limits: LogLimits) =
        LogLine(null, "warn", "log truncated at ${limits.maxStepBytes} bytes; later lines of this step are dropped")

    /** [text] within [max] UTF-8 bytes, and the bytes kept of it (the mark is not counted). */
    private fun cut(
        text: String,
        max: Int,
    ): Pair<String, Int> {
        var bytes = 0
        var end = 0
        while (end < text.length) {
            val point = text.codePointAt(end)
            val size = String(Character.toChars(point)).toByteArray().size
            if (bytes + size > max) return text.substring(0, end) + CUT to bytes
            bytes += size
            end += Character.charCount(point)
        }
        return text to bytes
    }
}
