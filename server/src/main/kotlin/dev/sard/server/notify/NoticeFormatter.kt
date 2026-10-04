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
import java.net.URI
import java.time.Duration
import java.time.Instant
import java.util.UUID

private const val BINARY = 1024
private const val SECONDS_PER_MINUTE = 60L
private const val SECONDS_PER_HOUR = 3600L
private const val NEWLINE = "\n"
private val WEB_SCHEMES = setOf("http", "https")
private const val MAX_REASON = 500
private const val ELLIPSIS = "…"

/** The language of notifications (`sard.notify.language`, S9b). */
enum class NoticeLanguage(
    val code: String,
) {
    EN("en"),
    RU("ru"),
    ;

    companion object {
        /** The language named by the setting; empty means the default, English. */
        fun of(setting: String): NoticeLanguage {
            val code = setting.trim().ifEmpty { EN.code }
            return requireNotNull(entries.firstOrNull { it.code == code }) {
                "sard.notify.language must be one of ${entries.joinToString { it.code }}, not \"$code\""
            }
        }
    }
}

/** The public address of the console (`sard.console.public-url`, S9b), without trailing slashes. */
class ConsoleUrl private constructor(
    private val base: String,
) {
    fun run(runId: UUID): String = "$base/runs/$runId"

    companion object {
        /** The address, or null when the setting is empty; anything but an http(s) address with a host fails. */
        fun parse(setting: String): ConsoleUrl? =
            setting
                .trim()
                .takeIf { it.isNotEmpty() }
                ?.let { ConsoleUrl(checked(it).trimEnd('/')) }

        private fun checked(address: String): String {
            val uri = runCatching { URI(address) }.getOrNull()
            require(uri != null && webAddress(uri) && plain(uri)) {
                "sard.console.public-url must be an absolute http or https address with a host, " +
                    "without a query or a fragment, not \"$address\""
            }
            return address
        }

        private fun webAddress(uri: URI) = uri.scheme in WEB_SCHEMES && !uri.host.isNullOrEmpty()

        private fun plain(uri: URI) = uri.rawQuery == null && uri.rawFragment == null
    }
}

/** The words of one language; the names of sources and agents are never translated. */
private class Wording(
    val agent: String,
    val duration: String,
    val total: String,
    val added: String,
    val run: String,
    val reason: String,
    val notGiven: String,
    val fullText: String,
    val connectionLost: String,
    val snapshotMade: String,
    val units: List<String>,
    val decimalSeparator: Char,
    val durationUnits: List<String>,
    val headlines: Map<StepState, Headline>,
)

private data class Headline(
    val icon: String,
    val text: String,
)

private val EN =
    Wording(
        agent = "Agent: ",
        duration = "Duration: ",
        total = "Total: ",
        added = ", added: ",
        run = "Run: ",
        reason = "Reason: ",
        notGiven = "Reason: not given",
        fullText = "The full text is in the console.",
        connectionLost =
            "The connection to the agent was interrupted while the step was running. The next run can be started.",
        snapshotMade =
            "The snapshot was created and can be restored, but part of the data did not get into it.",
        units = listOf("B", "KiB", "MiB", "GiB", "TiB"),
        decimalSeparator = '.',
        durationUnits = listOf("s", "min", "h"),
        headlines =
            mapOf(
                StepState.SUCCEEDED to Headline("✅", "Backup succeeded"),
                StepState.FAILED to Headline("❌", "Backup failed"),
                StepState.REJECTED to Headline("❌", "Backup rejected by the agent"),
                StepState.LOST to Headline("❌", "Backup lost"),
                StepState.TIMED_OUT to Headline("❌", "Backup timed out"),
                StepState.CANCELLED to Headline("⏹", "Backup cancelled"),
            ),
    )

private val RU =
    Wording(
        agent = "Агент: ",
        duration = "Длительность: ",
        total = "Всего: ",
        added = ", добавлено: ",
        run = "Запуск: ",
        reason = "Причина: ",
        notGiven = "Причина: не указана",
        fullText = "Полный текст — в консоли.",
        connectionLost = "Связь с агентом прервалась, пока шаг выполнялся. Можно запустить бэкап снова.",
        snapshotMade = "Снимок создан и пригоден для восстановления, но часть данных в него не попала.",
        units = listOf("Б", "КиБ", "МиБ", "ГиБ", "ТиБ"),
        decimalSeparator = ',',
        durationUnits = listOf("с", "мин", "ч"),
        headlines =
            mapOf(
                StepState.SUCCEEDED to Headline("✅", "Бэкап выполнен"),
                StepState.FAILED to Headline("❌", "Бэкап завершился ошибкой"),
                StepState.REJECTED to Headline("❌", "Бэкап отклонён агентом"),
                StepState.LOST to Headline("❌", "Бэкап потерян"),
                StepState.TIMED_OUT to Headline("❌", "Бэкап прерван по таймауту"),
                StepState.CANCELLED to Headline("⏹", "Бэкап отменён"),
            ),
    )

private fun wording(language: NoticeLanguage): Wording =
    when (language) {
        NoticeLanguage.EN -> EN
        NoticeLanguage.RU -> RU
    }

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
        if (text.codePointCount(0, text.length) <=
            MAX_REASON
        ) {
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
