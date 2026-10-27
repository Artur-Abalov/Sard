// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.notify

import dev.sard.server.runs.RestoreResult
import dev.sard.server.runs.RunKind
import dev.sard.server.runs.StepState
import dev.sard.server.runs.Trigger
import org.springframework.beans.factory.annotation.Value
import org.springframework.context.annotation.Bean
import org.springframework.context.annotation.Configuration
import java.math.BigDecimal
import java.math.RoundingMode
import java.time.Duration
import java.time.Instant
import java.time.ZoneOffset
import java.time.format.DateTimeFormatter
import java.util.UUID

private const val BINARY = 1024
private const val SECONDS_PER_MINUTE = 60L
private const val SECONDS_PER_HOUR = 3600L
private const val MAX_REASON = 500
private const val ELLIPSIS = "…"
private const val SHORT_ID = 8
private val MOMENT: DateTimeFormatter =
    DateTimeFormatter.ofPattern("yyyy-MM-dd HH:mm 'UTC'").withZone(ZoneOffset.UTC)

/** What a finished run looks like in a message (S9b); the wording is the specification's, per language. */
class RunNoticeFormatter(
    language: NoticeLanguage,
    private val console: ConsoleUrl?,
) : NotificationFormatter {
    private val words = wording(language)
    private val figures = NoticeFigures(words)
    private val lines = NoticeLines(words, figures, console)
    private val restores = RestoreNoticeFormatter(words, figures, lines)

    override fun format(notice: RunNotice): Message? {
        val telling = Telling.of(notice) ?: return null
        return when (notice.kind) {
            RunKind.RESTORE -> restore(notice)
            RunKind.BACKUP -> headline(notice, telling)?.let { compose(notice, it, telling) }
        }
    }

    /** A restore tells its own headline; here only whether its step is finished. */
    private fun restore(notice: RunNotice): Message? {
        val finished = words.headline(notice.stepStatus) != null
        return if (finished) restores.compose(notice) else null
    }

    private fun headline(
        notice: RunNotice,
        telling: Telling,
    ): Headline? {
        val recovered = Headline("✅", words.verdicts.recovered)
        return when {
            telling == Telling.RECOVERY -> recovered
            else -> words.headline(notice.stepStatus)
        }
    }

    private fun compose(
        notice: RunNotice,
        headline: Headline,
        telling: Telling,
    ): Message {
        val parts =
            listOf(
                listOf(listOf(Message.Bold("${headline.icon} ${headline.text}: ${notice.sourceName}"))),
                listOfNotNull(triggerLine(notice.trigger)),
                recoveryLines(telling, notice),
                listOf(listOf(Message.Text(words.agent), Message.Code(notice.agentHostname))),
                listOfNotNull(lines.durationLine(notice.startedAt, notice.finishedAt)),
                outcome(notice),
                lines.runLines(notice.runId),
            ).flatten()
        return Message.ofLines(parts)
    }

    /** A scheduled run says so on the line after the headline; a manual one does not (F3b). */
    private fun triggerLine(trigger: Trigger): List<Message.Part>? =
        when (trigger) {
            Trigger.SCHEDULE -> listOf(Message.Text(words.onSchedule))
            Trigger.CATCH_UP -> listOf(Message.Text(words.catchUpRun))
            Trigger.MANUAL, Trigger.VERIFICATION -> null
        }

    /** A recovery counts the failures it ends, unless a person started it: then it is a plain success. */
    private fun recoveryLines(
        telling: Telling,
        notice: RunNotice,
    ): List<List<Message.Part>> =
        if (telling == Telling.RECOVERY) {
            listOf(listOf(Message.Text("${words.failuresBefore}${notice.failuresBefore}")))
        } else {
            emptyList()
        }

    /** What came of the backup: its sizes, or why it failed and what is left of it; or the agent's last contact. */
    private fun outcome(notice: RunNotice): List<List<Message.Part>> =
        if (notice.stepStatus == StepState.AGENT_UNAVAILABLE) lines.agentLines(notice) else ran(notice)

    private fun ran(notice: RunNotice): List<List<Message.Part>> {
        val sizes = notice.backup?.let(::sizesLine)
        return when (notice.stepStatus) {
            StepState.SUCCEEDED -> {
                listOfNotNull(sizes)
            }

            StepState.LOST -> {
                lines.reasonLines(notice.message) + listOf(listOf(Message.Text(words.connectionLost)))
            }

            StepState.FAILED, StepState.REJECTED, StepState.TIMED_OUT, StepState.CANCELLED -> {
                val timeout = lines.timeoutLines(notice, words.timedOutHint)
                lines.reasonLines(notice.message) + timeout + snapshotLines(sizes)
            }

            StepState.QUEUED, StepState.DISPATCHED, StepState.RUNNING, StepState.AGENT_UNAVAILABLE -> {
                error("a run that ended has no active step: ${notice.stepStatus}")
            }
        }
    }

    private fun snapshotLines(sizes: List<Message.Part>?): List<List<Message.Part>> =
        if (sizes == null) emptyList() else listOf(listOf(Message.Text(words.snapshotMade)), sizes)

    private fun sizesLine(sizes: BackupSizes): List<Message.Part> {
        val total = figures.size(sizes.totalBytes)
        val added = figures.size(sizes.addedBytes)
        return listOf(Message.Text("${words.total}$total${words.added}$added"))
    }
}

/** What the restore step reported, null when it reported nothing. */
private val RunNotice.result get() = restore?.result

/** The lines a backup's message and a restore's share: the reason (cut to a limit), the duration and the run. */
internal class NoticeLines(
    private val words: Wording,
    private val figures: NoticeFigures,
    private val console: ConsoleUrl?,
) {
    /** The run's id, and its address in the console when there is one. */
    fun runLines(runId: UUID): List<List<Message.Part>> =
        listOf(listOf(Message.Text(words.run), Message.Code("$runId"))) +
            listOfNotNull(console?.let { listOf(Message.Text(it.run(runId))) })

    fun reasonLines(message: String?): List<List<Message.Part>> {
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

    /**
     * A step that timed out: the timeout it had, when it is known, and the two causes to look at (J1b, КJ-7); no
     * lines for a step that ended any other way.
     */
    fun timeoutLines(
        notice: RunNotice,
        hint: String,
    ): List<List<Message.Part>> {
        if (notice.stepStatus != StepState.TIMED_OUT) return emptyList()
        val timeout = notice.timeoutSeconds?.let { listOf(Message.Text(words.timeoutLine + figures.timeout(it))) }
        return listOfNotNull(timeout, listOf(Message.Text(hint)))
    }

    /** The run never started: when the agent was last in touch, or that it never was (J1c). */
    fun agentLines(notice: RunNotice): List<List<Message.Part>> {
        val since = notice.agentUnavailableSince
        val line = if (since == null) words.agentNeverConnected else words.agentOfflineSince + MOMENT.format(since)
        return listOf(listOf(Message.Text(line)))
    }

    fun durationLine(
        start: Instant?,
        end: Instant,
    ): List<Message.Part>? {
        val elapsed = start?.let { Duration.between(it, end) } ?: return null
        return listOf(Message.Text(words.duration + figures.duration(elapsed)))
    }
}

/** A restore's message (ВК39): what it restored, or why not and what is left; the lines it shares are the backup's. */
internal class RestoreNoticeFormatter(
    private val words: Wording,
    private val figures: NoticeFigures,
    private val lines: NoticeLines,
) {
    /** A restore (ВК39): what it restored, or why not and what is left. */
    fun compose(notice: RunNotice): Message {
        val headline =
            checkNotNull(words.headline(notice.stepStatus, words.restore.verdicts)) { "a finished restore" }
        val parts =
            listOf(
                listOf(listOf(Message.Bold("${headline.icon} ${headline.text}: ${notice.sourceName}"))),
                listOf(listOf(Message.Text(words.agent), Message.Code(notice.agentHostname))),
                listOfNotNull(snapshotLine(notice.restore)),
                listOfNotNull(lines.durationLine(notice.startedAt, notice.finishedAt)),
                restoreOutcome(notice),
                lines.runLines(notice.runId),
            ).flatten()
        return Message.ofLines(parts)
    }

    private fun snapshotLine(restore: RestoreNotice?): List<Message.Part>? =
        restore?.let {
            listOf(
                Message.Text(words.restore.snapshot),
                Message.Code(it.snapshotId.take(SHORT_ID)),
                Message.Text("${words.restore.snapshotAt}${it.snapshotCreatedAt}"),
            )
        }

    private fun restoreOutcome(notice: RunNotice): List<List<Message.Part>> =
        if (notice.stepStatus == StepState.AGENT_UNAVAILABLE) lines.agentLines(notice) else restoreRan(notice)

    private fun restoreRan(notice: RunNotice): List<List<Message.Part>> {
        val result = notice.result
        return when (notice.stepStatus) {
            StepState.SUCCEEDED -> {
                restoredLines(result)
            }

            StepState.LOST -> {
                lines.reasonLines(notice.message) + listOf(listOf(Message.Text(words.restore.lost)))
            }

            StepState.FAILED, StepState.REJECTED, StepState.TIMED_OUT, StepState.CANCELLED -> {
                val timeout = lines.timeoutLines(notice, words.restore.timedOutHint)
                lines.reasonLines(notice.message) + timeout + listOf(rollbackLine(result?.leftover))
            }

            StepState.QUEUED, StepState.DISPATCHED, StepState.RUNNING, StepState.AGENT_UNAVAILABLE -> {
                error("a restore that ended has no active step: ${notice.stepStatus}")
            }
        }
    }

    private fun restoredLines(r: RestoreResult?) = listOfNotNull(r?.let(::restoredLine))

    /** All or nothing: a failure left nothing, unless its rollback failed and the operator must clean up. */
    private fun rollbackLine(leftover: String?): List<Message.Part> =
        if (leftover.isNullOrEmpty()) {
            listOf(Message.Text(words.restore.nothingRestored))
        } else {
            listOf(
                Message.Text(words.restore.rollbackFailed),
                Message.Code(leftover),
                Message.Text(words.restore.removeByHand),
            )
        }

    private fun restoredLine(result: RestoreResult): List<Message.Part> =
        if (result.database != null) databaseLine(result.database, result.tablesRestored ?: 0) else filesLine(result)

    /** A restore of a database: its name and its tables, not its roles (РВ4). */
    private fun databaseLine(
        database: String,
        tables: Long,
    ): List<Message.Part> =
        listOf(
            Message.Text(words.restore.database),
            Message.Code(database),
            Message.Text("${words.restore.tables}$tables"),
        )

    private fun filesLine(result: RestoreResult): List<Message.Part> {
        val size = figures.size(result.bytesRestored ?: 0)
        val count = "${words.restore.filesRestored}${result.filesRestored ?: 0}${words.restore.size}$size"
        return listOf(Message.Text("$count${words.restore.where}"), Message.Code(result.target.orEmpty()))
    }
}

/** Durations and sizes as the console shows them (S9b); the formats repeat `web/src/format.ts`. */
internal class NoticeFigures(
    private val words: Wording,
) {
    fun duration(elapsed: Duration): String {
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

    /** A step timeout in the biggest unit that holds it whole, as the server's own messages tell it. */
    fun timeout(seconds: Long): String {
        val (s, min, h) = words.durationUnits
        return when {
            seconds % SECONDS_PER_HOUR == 0L -> "${seconds / SECONDS_PER_HOUR} $h"
            seconds % SECONDS_PER_MINUTE == 0L -> "${seconds / SECONDS_PER_MINUTE} $min"
            else -> "$seconds $s"
        }
    }

    /** Binary units, at most one decimal, half up, no trailing ",0" — as the console shows it. */
    fun size(bytes: Long): String {
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

/** The formatter bean (S9b): always present; a bad setting stops the start. */
@Configuration(proxyBeanMethods = false)
class NoticeFormatterConfiguration {
    @Bean
    fun noticeFormatter(
        @Value("\${sard.notify.language:en}") language: String,
        @Value("\${sard.console.public-url:}") publicUrl: String,
    ): NotificationFormatter = RunNoticeFormatter(NoticeLanguage.of(language), ConsoleUrl.parse(publicUrl))

    /** The alert about fires skipped in a row (F3b): the same language and console address. */
    @Bean
    fun skipAlertFormatter(
        @Value("\${sard.notify.language:en}") language: String,
        @Value("\${sard.console.public-url:}") publicUrl: String,
    ): SkipAlertFormatter = ScheduleAlertFormatter(NoticeLanguage.of(language), ConsoleUrl.parse(publicUrl))

    /** An overdue episode (F6): the same language and console address. */
    @Bean
    fun overdueFormatter(
        @Value("\${sard.notify.language:en}") language: String,
        @Value("\${sard.console.public-url:}") publicUrl: String,
    ): OverdueFormatter = OverdueNoticeFormatter(NoticeLanguage.of(language), ConsoleUrl.parse(publicUrl))

    /** An agent certificate that reached a threshold or expired (C1 phase 3): the same language and console address. */
    @Bean
    fun certificateFormatter(
        @Value("\${sard.notify.language:en}") language: String,
        @Value("\${sard.console.public-url:}") publicUrl: String,
    ): CertificateFormatter = CertificateNoticeFormatter(NoticeLanguage.of(language), ConsoleUrl.parse(publicUrl))
}
