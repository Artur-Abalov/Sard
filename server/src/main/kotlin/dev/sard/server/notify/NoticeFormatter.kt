// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.notify

import dev.sard.server.runs.StepState
import dev.sard.server.runs.Trigger
import org.springframework.beans.factory.annotation.Value
import org.springframework.context.annotation.Bean
import org.springframework.context.annotation.Configuration
import java.math.BigDecimal
import java.math.RoundingMode
import java.time.Duration
import java.time.Instant

private const val BINARY = 1024
private const val SECONDS_PER_MINUTE = 60L
private const val SECONDS_PER_HOUR = 3600L
private const val NEWLINE = "\n"
private const val MAX_REASON = 500
private const val ELLIPSIS = "…"

/** What a finished run looks like in a message (S9b); the wording is the specification's, per language. */
class RunNoticeFormatter(
    language: NoticeLanguage,
    private val console: ConsoleUrl?,
) : NotificationFormatter {
    private val words = wording(language)

    override fun format(notice: RunNotice): Message? {
        val headline = words.headlines[notice.stepStatus]
        return if (notice.trigger == Trigger.MANUAL && headline != null) compose(notice, headline) else null
    }

    private fun compose(
        notice: RunNotice,
        headline: Headline,
    ): Message {
        val lines =
            listOf(
                listOf(listOf(Message.Bold("${headline.icon} ${headline.text}: ${notice.sourceName}"))),
                listOf(listOf(Message.Text(words.agent), Message.Code(notice.agentHostname))),
                listOfNotNull(durationLine(notice.startedAt, notice.finishedAt)),
                outcome(notice),
                listOf(listOf(Message.Text(words.run), Message.Code("${notice.runId}"))),
                listOfNotNull(console?.let { listOf(Message.Text(it.run(notice.runId))) }),
            ).flatten()
        val newline = listOf(Message.Text(NEWLINE))
        return Message(lines.flatMapIndexed { index, line -> if (index == 0) line else newline + line })
    }

    /** What came of the backup: its sizes, or why it failed and what is left of it. */
    private fun outcome(notice: RunNotice): List<List<Message.Part>> {
        val sizes = notice.backup?.let(::sizesLine)
        return when (notice.stepStatus) {
            StepState.SUCCEEDED -> listOfNotNull(sizes)
            StepState.LOST -> reasonLines(notice.message) + listOf(listOf(Message.Text(words.connectionLost)))
            else -> reasonLines(notice.message) + snapshotLines(sizes)
        }
    }

    private fun snapshotLines(sizes: List<Message.Part>?): List<List<Message.Part>> =
        if (sizes == null) emptyList() else listOf(listOf(Message.Text(words.snapshotMade)), sizes)

    private fun reasonLines(message: String?): List<List<Message.Part>> {
        val text = message.orEmpty()
        if (text.isBlank()) return listOf(listOf(Message.Text(words.notGiven)))
        val shown = limited(text)
        val line = listOf(Message.Text(words.reason), Message.Code(shown))
        return if (shown == text) listOf(line) else listOf(line, listOf(Message.Text(words.fullText)))
    }

    private fun limited(text: String): String =
        if (text.codePointCount(0, text.length) <= MAX_REASON) {
            text
        } else {
            text.substring(0, text.offsetByCodePoints(0, MAX_REASON)) + ELLIPSIS
        }

    private fun durationLine(
        start: Instant?,
        end: Instant,
    ): List<Message.Part>? = start?.let { listOf(Message.Text(words.duration + duration(Duration.between(it, end)))) }

    private fun duration(elapsed: Duration): String {
        val seconds = elapsed.seconds.coerceAtLeast(0)
        val (s, min, h) = words.durationUnits
        return when {
            seconds >= SECONDS_PER_HOUR -> {
                "${seconds / SECONDS_PER_HOUR} $h ${seconds % SECONDS_PER_HOUR / SECONDS_PER_MINUTE} $min"
            }

            seconds >= SECONDS_PER_MINUTE -> {
                "${seconds / SECONDS_PER_MINUTE} $min ${seconds % SECONDS_PER_MINUTE} $s"
            }

            else -> {
                "$seconds $s"
            }
        }
    }

    private fun sizesLine(sizes: BackupSizes): List<Message.Part> =
        listOf(Message.Text("${words.total}${size(sizes.totalBytes)}${words.added}${size(sizes.addedBytes)}"))

    /** Binary units, at most one decimal, half up, no trailing ",0" — as the console shows it. */
    private fun size(bytes: Long): String {
        var value = bytes.toDouble()
        var unit = 0
        while (value >= BINARY && unit < words.units.size - 1) {
            value /= BINARY
            unit++
        }
        val rounded =
            BigDecimal
                .valueOf(value)
                .setScale(1, RoundingMode.HALF_UP)
                .toPlainString()
                .removeSuffix(".0")
        return "${rounded.replace('.', words.decimalSeparator)} ${words.units[unit]}"
    }
}

/** The formatter bean (S9b): without it notifications are off; a bad setting stops the start. */
@Configuration(proxyBeanMethods = false)
class NoticeFormatterConfiguration {
    @Bean
    fun noticeFormatter(
        @Value("\${sard.notify.language:en}") language: String,
        @Value("\${sard.console.public-url:}") publicUrl: String,
    ): NotificationFormatter = RunNoticeFormatter(NoticeLanguage.of(language), ConsoleUrl.parse(publicUrl))
}
