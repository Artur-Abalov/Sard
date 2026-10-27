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
    /** Before the timeout of a step that timed out (J1b), and the two causes a person should look at (КJ-7, Р-J9). */
    val timeoutLine: String,
    val timedOutHint: String,
    /** Before the moment the agent was last in touch, and when it never was (J1c). */
    val agentOfflineSince: String,
    val agentNeverConnected: String,
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

            StepState.AGENT_UNAVAILABLE -> {
                Headline("⏸", of.agentUnavailable)
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
    val timedOutHint: String,
)

/** What each finished step is called in a headline. */
internal class Verdicts(
    val succeeded: String,
    val failed: String,
    val rejected: String,
    val lost: String,
    val timedOut: String,
    val cancelled: String,
    /** The run did not start: its agent was offline until the wait deadline (J1c); not a failure. */
    val agentUnavailable: String,
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

            StepState.SUCCEEDED,
            StepState.CANCELLED,
            StepState.QUEUED,
            StepState.DISPATCHED,
            StepState.RUNNING,
            StepState.AGENT_UNAVAILABLE,
            -> {
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
            "The snapshot was created and can be restored, but part of the data did not get into it.",
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
                agentUnavailable = "Backup not run — the agent was unavailable",
                recovered = "Backup is working again",
            ),
        onSchedule = "On schedule",
        catchUpRun = "Catch-up run after server downtime",
        failuresBefore = "Failures in a row before this: ",
        timeoutLine = "Step timeout: ",
        timedOutHint =
            "If the storage is unreachable, check the connection to it. " +
                "If the backup is long, raise the source's timeout.",
        agentOfflineSince = "Agent offline since ",
        agentNeverConnected = "The agent has never connected",
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
                        agentUnavailable = "Restore not run — the agent was unavailable",
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
                timedOutHint =
                    "If the storage is unreachable, check the connection to it. " +
                        "If the restore is long, raise the source's timeout.",
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
        verdicts =
            Verdicts(
                succeeded = "Бэкап выполнен",
                failed = "Бэкап завершился ошибкой",
                rejected = "Бэкап отклонён агентом",
                lost = "Бэкап потерян",
                timedOut = "Бэкап прерван по таймауту",
                cancelled = "Бэкап отменён",
                agentUnavailable = "Бэкап не выполнен — агент был недоступен",
                recovered = "Бэкап снова работает",
            ),
        onSchedule = "По расписанию",
        catchUpRun = "Догоняющий запуск после простоя сервера",
        failuresBefore = "Ошибок подряд перед этим: ",
        timeoutLine = "Срок выполнения: ",
        timedOutHint =
            "Хранилище недоступно — проверьте связь с ним. Бэкап долгий — увеличьте срок у источника.",
        agentOfflineSince = "Агент не на связи с ",
        agentNeverConnected = "Агент ещё ни разу не подключался",
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
                        agentUnavailable = "Восстановление не выполнено — агент был недоступен",
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
                timedOutHint =
                    "Хранилище недоступно — проверьте связь с ним. " +
                        "Восстановление долгое — увеличьте срок у источника.",
            ),
    )

internal fun wording(language: NoticeLanguage): Wording =
    when (language) {
        NoticeLanguage.EN -> EN
        NoticeLanguage.RU -> RU
    }
