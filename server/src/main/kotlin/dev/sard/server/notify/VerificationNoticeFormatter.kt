// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.notify

import dev.sard.server.runs.StageState
import dev.sard.server.runs.StepOutput
import dev.sard.server.verify.MismatchWording
import dev.sard.server.verify.VerificationStatus
import java.time.Duration
import java.time.ZoneId
import java.time.format.DateTimeFormatter

private const val SHOWN_MISMATCHES = 5
private const val SHOWN_VALUE = 120
private const val SHORT_SNAPSHOT = 8
private const val ELLIPSIS = "…"
private const val COMPARISON_STAGE = "fingerprint"
private const val RESTORE_TIME_LEVEL = 5
private const val GROUP = 3
private val INTEGER = Regex("\\d+")

/** The words for each outcome of a verification (Т6), by [VerificationStatus]. */
private class Outcomes(
    val failed: String,
    val cannotVerify: String,
    val infrastructureFailure: String,
    val noVerifier: String,
    val passed: String,
) {
    fun of(status: VerificationStatus): String =
        when (status) {
            VerificationStatus.FAILED -> failed
            VerificationStatus.CANNOT_VERIFY -> cannotVerify
            VerificationStatus.INFRASTRUCTURE_FAILURE -> infrastructureFailure
            VerificationStatus.NO_VERIFIER -> noVerifier
            VerificationStatus.PASSED -> passed
        }
}

/** A mismatch said in words; each takes the values already cut and formatted (Т5). */
private class MismatchWords(
    val rows: (table: String, expected: String, actual: String) -> String,
    val tables: (expected: String, actual: String) -> String,
    val controlEmpty: (path: String) -> String,
    val controlMissing: (path: String) -> String,
    val files: (expected: String, actual: String) -> String,
    val bytes: (expected: String, actual: String) -> String,
    val other: (name: String, expected: String, actual: String) -> String,
)

/** The words of the messages about verification in one language; names of sources and hosts are never translated. */
private class VerificationWords(
    val failed: String,
    val recovered: String,
    val stale: (days: Int) -> String,
    val noVerifierTitle: String,
    val stage: String,
    val snapshot: String,
    val verifier: String,
    val mismatches: String,
    val more: (count: Int) -> String,
    val verification: String,
    val level: String,
    val restoreTime: String,
    val lastPass: String,
    val neverPassed: String,
    val lastOutcome: String,
    val sourcesToVerify: String,
    val noVerifierAdvice: String,
    val thousands: String,
    val outcomes: Outcomes,
    val levels: List<String>,
    val stages: Map<String, String>,
    val mismatchWords: MismatchWords,
)

private val EN =
    VerificationWords(
        failed = "Restore verification failed",
        recovered = "Restore verification passed again",
        stale = { "Restore not verified for over $it d" },
        noVerifierTitle = "Restore verification is not working: no verifier",
        stage = "Stage: ",
        snapshot = "Snapshot: ",
        verifier = "Verifier: ",
        mismatches = "Mismatches:",
        more = { "and $it more" },
        verification = "Verification: ",
        level = "Level: ",
        restoreTime = "Time to restore: ",
        lastPass = "Last successful verification: ",
        neverPassed = "No successful verification yet",
        lastOutcome = "Last outcome: ",
        sourcesToVerify = "Sources to verify: ",
        noVerifierAdvice = "Issue a token with the verifier role and install an agent: see the console overview",
        thousands = ",",
        outcomes =
            Outcomes(
                failed = "failed",
                cannotVerify = "cannot verify",
                infrastructureFailure = "infrastructure failure",
                noVerifier = "no fitting verifier",
                passed = "passed",
            ),
        levels = listOf("integrity", "extraction", "application start", "data matches", "restore time measured"),
        stages =
            mapOf(
                "snapshot" to "snapshot",
                "fingerprint_tag" to "fingerprint tag",
                "work_volume" to "work volume",
                "restic_check" to "storage integrity",
                "sandbox" to "sandbox",
                "restore" to "extraction",
                "start" to "application start",
                "fingerprint" to "fingerprint comparison",
                "cleanup" to "cleanup",
            ),
        mismatchWords =
            MismatchWords(
                rows = { table, e, a -> "table $table: $e rows in the source, $a in the copy" },
                tables = { e, a -> "tables: $e in the source, $a in the copy" },
                controlEmpty = { "control path $it is empty" },
                controlMissing = { "control path $it is missing in the copy" },
                files = { e, a -> "files: $e in the source, $a in the copy" },
                bytes = { e, a -> "size: $e in the source, $a in the copy" },
                other = { name, e, a -> "$name: $e in the source, $a in the copy" },
            ),
    )

private val RU =
    VerificationWords(
        failed = "Проверка восстановления провалилась",
        recovered = "Проверка восстановления снова прошла",
        stale = { "Восстановление не проверено дольше $it дн." },
        noVerifierTitle = "Проверка восстановления не работает: нет верификатора",
        stage = "Стадия: ",
        snapshot = "Снимок: ",
        verifier = "Верификатор: ",
        mismatches = "Расхождения:",
        more = { "и ещё $it" },
        verification = "Проверка: ",
        level = "Уровень: ",
        restoreTime = "Время восстановления: ",
        lastPass = "Последняя успешная проверка: ",
        neverPassed = "Успешных проверок ещё не было",
        lastOutcome = "Последний итог: ",
        sourcesToVerify = "Источников с включённой проверкой: ",
        noVerifierAdvice = "Выпустите токен с ролью «верификатор» и поставьте агента: инструкция на обзоре консоли",
        thousands = " ",
        outcomes =
            Outcomes(
                failed = "провал",
                cannotVerify = "не можем проверить",
                infrastructureFailure = "сбой инфраструктуры",
                noVerifier = "нет подходящего верификатора",
                passed = "пройдено",
            ),
        levels = listOf("целостность", "извлечение", "запуск", "данные совпали", "время восстановления измерено"),
        stages =
            mapOf(
                "snapshot" to "снимок",
                "fingerprint_tag" to "отпечаток в теге снимка",
                "work_volume" to "рабочий том",
                "restic_check" to "целостность хранилища",
                "sandbox" to "песочница",
                "restore" to "извлечение",
                "start" to "запуск приложения",
                "fingerprint" to "сравнение отпечатков",
                "cleanup" to "уборка",
            ),
        mismatchWords =
            MismatchWords(
                rows = { table, e, a -> "таблица $table: в источнике $e строк, в копии $a" },
                tables = { e, a -> "таблиц: в источнике $e, в копии $a" },
                controlEmpty = { "контрольный путь $it пуст" },
                controlMissing = { "контрольного пути $it нет в копии" },
                files = { e, a -> "файлов: в источнике $e, в копии $a" },
                bytes = { e, a -> "объём: в источнике $e, в копии $a" },
                other = { name, e, a -> "$name: в источнике $e, в копии $a" },
            ),
    )

/**
 * What a verification record, a stale episode and the «no verifier» state say in Telegram (V6, Т1–Т9 of
 * verification-notifications.feature): names and facts only, never the output of a stage, a config or a repository.
 */
class VerificationNoticeFormatter(
    language: NoticeLanguage,
    private val console: ConsoleUrl?,
) : VerificationFormatter {
    private val words =
        when (language) {
            NoticeLanguage.EN -> EN
            NoticeLanguage.RU -> RU
        }
    private val reasons = NoticeLines(wording(language), NoticeFigures(wording(language)), console)
    private val figures = NoticeFigures(wording(language))
    private val sentences = MismatchSentences(words.mismatchWords, words.thousands, figures)

    override fun verification(notice: VerificationNotice): Message? =
        when (VerificationTelling.of(notice)) {
            VerificationTelling.FIRST_FAILURE -> failure(notice)
            VerificationTelling.RECOVERY -> recovery(notice)
            null -> null
        }

    override fun stale(notice: StaleNotice): Message {
        val zone = ZoneId.of(notice.timezone)
        val moment = DateTimeFormatter.ofPattern("yyyy-MM-dd HH:mm").withZone(zone)
        val last =
            notice.lastPassAt?.let { "${words.lastPass}${moment.format(it)} ${notice.timezone}" } ?: words.neverPassed
        return Message.ofLines(
            listOfNotNull(
                listOf(Message.Bold("⚠️ ${words.stale(notice.thresholdDays)}: ${notice.sourceName}")),
                listOf(Message.Text(last)),
                notice.lastOutcome?.let { listOf(Message.Text(words.lastOutcome + words.outcomes.of(it))) },
                console?.let { listOf(Message.Text(it.source(notice.sourceId))) },
            ),
        )
    }

    override fun noVerifier(notice: NoVerifierNotice): Message =
        Message.ofLines(
            listOfNotNull(
                listOf(Message.Bold("⚠️ ${words.noVerifierTitle}")),
                listOf(Message.Text("${words.sourcesToVerify}${notice.sources}")),
                listOf(Message.Text(words.noVerifierAdvice)),
                console?.let { listOf(Message.Text(it.overview())) },
            ),
        )

    private fun failure(notice: VerificationNotice): Message =
        Message.ofLines(
            listOfNotNull(
                listOf(Message.Bold("❌ ${words.failed}: ${notice.sourceName}")),
                stageLine(notice),
                listOf(Message.Text(words.snapshot), Message.Code(notice.snapshotId.take(SHORT_SNAPSHOT))),
                notice.verifierHostname?.let { listOf(Message.Text(words.verifier), Message.Code(it)) },
            ) + whatDiffers(notice) + recordLines(notice),
        )

    private fun recovery(notice: VerificationNotice): Message =
        Message.ofLines(
            listOfNotNull(
                listOf(Message.Bold("✅ ${words.recovered}: ${notice.sourceName}")),
                listOf(Message.Text(words.level + levelLine(notice.level))),
                restoreTimeLine(notice),
            ) + recordLines(notice),
        )

    /** The record's own id and, with a console address, its page. */
    private fun recordLines(notice: VerificationNotice): List<List<Message.Part>> =
        listOfNotNull(
            listOf(Message.Text(words.verification), Message.Code("${notice.verificationId}")),
            console?.let { listOf(Message.Text(it.verification(notice.verificationId))) },
        )

    private fun levelLine(level: Int): String = "$level — ${words.levels[level - 1]}"

    private fun restoreTimeLine(notice: VerificationNotice): List<Message.Part>? =
        notice.restoreMillis
            ?.takeIf { notice.level == RESTORE_TIME_LEVEL }
            ?.let { listOf(Message.Text(words.restoreTime + figures.duration(Duration.ofMillis(it)))) }

    /** The first failed stage; without one, the comparison when it found mismatches; otherwise nothing is known. */
    private fun stageLine(notice: VerificationNotice): List<Message.Part>? {
        val failed = notice.stages.firstOrNull { it.state == StageState.FAILED }?.name
        val name = failed ?: COMPARISON_STAGE.takeIf { notice.mismatches.isNotEmpty() } ?: return null
        return listOf(Message.Text(words.stage + (words.stages[name] ?: name)))
    }

    /** The mismatches in words, or the reason when the verifier named none. */
    private fun whatDiffers(notice: VerificationNotice): List<List<Message.Part>> {
        if (notice.mismatches.isEmpty()) return reasons.reasonLines(notice.reason)
        val shown = notice.mismatches.take(SHOWN_MISMATCHES).map { listOf(Message.Text("— ${sentences.of(it)}")) }
        val rest = notice.mismatches.size - SHOWN_MISMATCHES
        val more = if (rest > 0) listOf(listOf(Message.Text("— ${words.more(rest)}"))) else emptyList()
        return listOf(listOf(Message.Text(words.mismatches))) + shown + more
    }
}

/** A mismatch said in words (Т5): values cut to 120 characters, counts in digit groups, sizes as S9b writes them. */
private class MismatchSentences(
    private val words: MismatchWords,
    private val thousands: String,
    private val figures: NoticeFigures,
) {
    fun of(mismatch: StepOutput.Mismatch): String {
        val wording = MismatchWording.of(mismatch.name)
        val expected = cut(mismatch.expected)
        val actual = cut(mismatch.actual)
        return when (wording.kind) {
            "rows" -> words.rows(wording.subject, count(expected), count(actual))
            "tables" -> words.tables(count(expected), count(actual))
            "files" -> words.files(count(expected), count(actual))
            "bytes" -> words.bytes(size(expected), size(actual))
            "control_path" -> controlPath(wording.subject, mismatch)
            else -> words.other(mismatch.name, expected, actual)
        }
    }

    /** A control path that is empty or missing in the copy; any other difference is said as the generic one. */
    private fun controlPath(
        path: String,
        mismatch: StepOutput.Mismatch,
    ): String =
        when (mismatch.actual) {
            "empty" -> words.controlEmpty(path)
            "missing" -> words.controlMissing(path)
            else -> words.other(mismatch.name, cut(mismatch.expected), cut(mismatch.actual))
        }

    private fun cut(value: String): String =
        if (value.codePointCount(0, value.length) <= SHOWN_VALUE) {
            value
        } else {
            value.substring(0, value.offsetByCodePoints(0, SHOWN_VALUE)) + ELLIPSIS
        }

    /** A count with its digits in groups; a value that is no whole number stays as it is. */
    private fun count(value: String): String =
        if (INTEGER.matches(value)) {
            value
                .reversed()
                .chunked(GROUP)
                .joinToString(thousands)
                .reversed()
        } else {
            value
        }

    private fun size(value: String): String = value.toLongOrNull()?.let(figures::size) ?: value
}
