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
) {
    /** The headline of a finished step; null for a step still active (nothing to announce). */
    fun headline(step: StepState): Headline? =
        when (step) {
            StepState.SUCCEEDED -> {
                Headline("✅", verdicts.succeeded)
            }

            StepState.CANCELLED -> {
                Headline("⏹", verdicts.cancelled)
            }

            StepState.FAILED, StepState.REJECTED, StepState.LOST, StepState.TIMED_OUT -> {
                Headline("❌", verdicts.failure(step))
            }

            StepState.QUEUED, StepState.DISPATCHED, StepState.RUNNING -> {
                null
            }
        }
}

/** What each finished step is called in a headline. */
internal class Verdicts(
    val succeeded: String,
    val failed: String,
    val rejected: String,
    val lost: String,
    val timedOut: String,
    val cancelled: String,
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
            ),
    )

internal fun wording(language: NoticeLanguage): Wording =
    when (language) {
        NoticeLanguage.EN -> EN
        NoticeLanguage.RU -> RU
    }
