// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.notify

import dev.sard.server.runs.StepState
import org.springframework.beans.factory.annotation.Value
import org.springframework.context.annotation.Bean
import org.springframework.context.annotation.Configuration
import java.math.BigDecimal
import java.math.RoundingMode
import java.time.Duration
import java.time.Instant
import java.time.ZoneOffset
import java.time.format.DateTimeFormatter

private const val BINARY = 1024
private const val SECONDS_PER_MINUTE = 60L
private const val SECONDS_PER_HOUR = 3600L
private const val NEWLINE = "\n"
private const val MAX_REASON = 500
private const val ELLIPSIS = "…"

/** Times in a message: UTC to the minute, the same for every reader of a shared chat. */
private val MOMENT: DateTimeFormatter = DateTimeFormatter.ofPattern("yyyy-MM-dd HH:mm 'UTC'").withZone(ZoneOffset.UTC)

/**
 * What a finished run (S9b) or an overdue episode (F6) looks like in a message; the wording is the
 * specification's, per language.
 */
class RunNoticeFormatter(
    language: NoticeLanguage,
    private val console: ConsoleUrl?,
) : NotificationFormatter {
    private val words = wording(language)

    private val overdue = OverdueFormatter(words, console)

    override fun format(notice: Notice): Message? =
        when (notice) {
            is RunNotice -> Telling.of(notice)?.let { headline(notice, it) }?.let { compose(notice, it) }
            is OverdueNotice -> overdue.format(notice)
        }

    private fun headline(
        notice: RunNotice,
        telling: Telling,
    ): Headline? {
        val recovered = Headline("✅", words.verdicts.recovered)
        return if (telling == Telling.RECOVERY) recovered else words.headline(notice.stepStatus)
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
        return messageOf(lines)
    }

    /** What came of the backup: its sizes, or why it failed and what is left of it. */
    private fun outcome(notice: RunNotice): List<List<Message.Part>> {
        val sizes = notice.backup?.let(::sizesLine)
        return when (notice.stepStatus) {
            StepState.SUCCEEDED -> {
                listOfNotNull(sizes)
            }

            StepState.LOST -> {
                reasonLines(notice.message) + listOf(listOf(Message.Text(words.connectionLost)))
            }

            StepState.FAILED, StepState.REJECTED, StepState.TIMED_OUT, StepState.CANCELLED -> {
                reasonLines(notice.message) + snapshotLines(sizes)
            }

            StepState.QUEUED, StepState.DISPATCHED, StepState.RUNNING -> {
                error("a finished run has no active step: ${notice.stepStatus}")
            }
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

/** An overdue episode in a message (F6, ADR 00XX-draft-source-freshness). */
internal class OverdueFormatter(
    private val words: Wording,
    private val console: ConsoleUrl?,
) {
    /**
     * An episode that opened; one a success closed, unless that run is told about itself (a manual result, a
     * recovery): one message per turn of events.
     */
    fun format(notice: OverdueNotice): Message? {
        val closing = notice.closedBy ?: return messageOf(opened(notice))
        return if (Telling.of(closing) == null) messageOf(resumed(notice, closing)) else null
    }

    private fun opened(notice: OverdueNotice): List<List<Message.Part>> {
        val overdue = words.overdue
        val last = notice.countedFrom?.let { Message.Text(overdue.lastSuccess + MOMENT.format(it)) }
        return listOf(
            listOf(Message.Bold("⚠️ ${overdue.opened}: ${notice.sourceName}")),
            listOf(Message.Text(words.agent), Message.Code(notice.agentHostname)),
            listOf(last ?: Message.Text(overdue.never)),
            listOf(Message.Text(overdue.expectedBy + MOMENT.format(notice.deadline))),
        )
    }

    private fun resumed(
        notice: OverdueNotice,
        closing: RunNotice,
    ): List<List<Message.Part>> =
        listOfNotNull(
            listOf(Message.Bold("✅ ${words.overdue.resumed}: ${notice.sourceName}")),
            listOf(Message.Text(words.agent), Message.Code(notice.agentHostname)),
            listOf(Message.Text(words.run), Message.Code("${closing.runId}")),
            console?.let { listOf(Message.Text(it.run(closing.runId))) },
        )
}

/** Lines joined by newlines, each a list of parts. */
internal fun messageOf(lines: List<List<Message.Part>>): Message {
    val newline = listOf(Message.Text(NEWLINE))
    return Message(lines.flatMapIndexed { index, line -> if (index == 0) line else newline + line })
}

/** The formatter bean (S9b): always present; a bad setting stops the start. */
@Configuration(proxyBeanMethods = false)
class NoticeFormatterConfiguration {
    @Bean
    fun noticeFormatter(
        @Value("\${sard.notify.language:en}") language: String,
        @Value("\${sard.console.public-url:}") publicUrl: String,
    ): NotificationFormatter = RunNoticeFormatter(NoticeLanguage.of(language), ConsoleUrl.parse(publicUrl))
}
