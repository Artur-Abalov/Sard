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
    val headlines: Map<StepState, Headline>,
)

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

internal fun wording(language: NoticeLanguage): Wording =
    when (language) {
        NoticeLanguage.EN -> EN
        NoticeLanguage.RU -> RU
    }
