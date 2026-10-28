// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.notify

import dev.sard.server.runs.StepState

/** The words of one language; the names of sources and agents are never translated. */
internal class Wording(
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
    val verdicts: Verdicts,
    /** The line after the headline of a scheduled run (F3b). */
    val onSchedule: String,
    val catchUpRun: String,
    val failuresBefore: String,
    /** The words of a restore (R2). */
    val restore: RestoreWording,
) {
    /** The headline of a finished step; null for a step still active (nothing to announce). */
    fun headline(
        step: StepState,
        of: Verdicts = verdicts,
    ): Headline? =
        when (step) {
            StepState.SUCCEEDED -> {
                Headline("✅", of.succeeded)
            }

            StepState.CANCELLED -> {
                Headline("⏹", of.cancelled)
            }

            StepState.FAILED, StepState.REJECTED, StepState.LOST, StepState.TIMED_OUT -> {
                Headline("❌", of.failure(step))
            }

            StepState.QUEUED, StepState.DISPATCHED, StepState.RUNNING -> {
                null
            }
        }
}

/** What a restore says that a backup does not (ВК39); the lines it shares with a backup use [Wording]. */
internal class RestoreWording(
    val verdicts: Verdicts,
    val snapshot: String,
    /** Between the short id and the time of the snapshot. */
    val snapshotAt: String,
    val filesRestored: String,
    val size: String,
    val where: String,
    /** Before the name of the database a restore of a database made, and before the number of its tables. */
    val database: String,
    val tables: String,
    val nothingRestored: String,
    val rollbackFailed: String,
    val removeByHand: String,
    val lost: String,
)

/** What each finished step is called in a headline. */
internal class Verdicts(
    val succeeded: String,
    val failed: String,
    val rejected: String,
    val lost: String,
    val timedOut: String,
    val cancelled: String,
    /** A scheduled run that succeeded after failed ones (ADR 0024). */
    val recovered: String,
) {
    fun failure(step: StepState): String =
        when (step) {
            StepState.FAILED -> {
                failed
            }

            StepState.REJECTED -> {
                rejected
            }

            StepState.LOST -> {
                lost
            }

            StepState.TIMED_OUT -> {
                timedOut
            }

            StepState.SUCCEEDED, StepState.CANCELLED, StepState.QUEUED, StepState.DISPATCHED, StepState.RUNNING -> {
                error("not a failure: $step")
            }
        }
}

internal data class Headline(
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
            "The snapshot was created but is incomplete: part of the data did not get into it. " +
                "It can be restored only on the host, with repo restore.",
        units = listOf("B", "KiB", "MiB", "GiB", "TiB"),
        decimalSeparator = '.',
        durationUnits = listOf("s", "min", "h"),
        verdicts =
            Verdicts(
                succeeded = "Backup succeeded",
                failed = "Backup failed",
                rejected = "Backup rejected by the agent",
                lost = "Backup lost",
                timedOut = "Backup timed out",
                cancelled = "Backup cancelled",
                recovered = "Backup is working again",
            ),
        onSchedule = "On schedule",
        catchUpRun = "Catch-up run after server downtime",
        failuresBefore = "Failures in a row before this: ",
        restore =
            RestoreWording(
                verdicts =
                    Verdicts(
                        succeeded = "Restore succeeded",
                        failed = "Restore failed",
                        rejected = "Restore rejected by the agent",
                        lost = "Restore lost",
                        timedOut = "Restore timed out",
                        cancelled = "Restore cancelled",
                        recovered = "Restore succeeded",
                    ),
                snapshot = "Snapshot: ",
                snapshotAt = " of ",
                filesRestored = "Files restored: ",
                size = ", size: ",
                where = ". Where: ",
                database = "Database: ",
                tables = ", tables: ",
                nothingRestored = "Nothing was restored: the changes were rolled back.",
                rollbackFailed = "Rollback failed, left behind: ",
                removeByHand = " Remove it by hand.",
                lost =
                    "The connection to the agent was interrupted while the step was running. " +
                        "The restore can be started again.",
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
        snapshotMade =
            "Снимок создан, но неполный: часть данных в него не попала. " +
                "Восстановить его можно только на хосте командой repo restore.",
        units = listOf("Б", "КиБ", "МиБ", "ГиБ", "ТиБ"),
        decimalSeparator = ',',
        durationUnits = listOf("с", "мин", "ч"),
        verdicts =
            Verdicts(
                succeeded = "Бэкап выполнен",
                failed = "Бэкап завершился ошибкой",
                rejected = "Бэкап отклонён агентом",
                lost = "Бэкап потерян",
                timedOut = "Бэкап прерван по таймауту",
                cancelled = "Бэкап отменён",
                recovered = "Бэкап снова работает",
            ),
        onSchedule = "По расписанию",
        catchUpRun = "Догоняющий запуск после простоя сервера",
        failuresBefore = "Ошибок подряд перед этим: ",
        restore =
            RestoreWording(
                verdicts =
                    Verdicts(
                        succeeded = "Восстановление выполнено",
                        failed = "Восстановление завершилось ошибкой",
                        rejected = "Восстановление отклонено агентом",
                        lost = "Восстановление потеряно",
                        timedOut = "Восстановление прервано по таймауту",
                        cancelled = "Восстановление отменено",
                        recovered = "Восстановление выполнено",
                    ),
                snapshot = "Снимок: ",
                snapshotAt = " от ",
                filesRestored = "Восстановлено файлов: ",
                size = ", объём: ",
                where = ". Куда: ",
                database = "База: ",
                tables = ", таблиц: ",
                nothingRestored = "Ничего не восстановлено: изменения отменены.",
                rollbackFailed = "Откат не удался, осталось: ",
                removeByHand = " Удалите вручную.",
                lost = "Связь с агентом прервалась, пока шаг выполнялся. Можно повторить восстановление.",
            ),
    )

internal fun wording(language: NoticeLanguage): Wording =
    when (language) {
        NoticeLanguage.EN -> EN
        NoticeLanguage.RU -> RU
    }
