// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.notify

import dev.sard.server.scheduler.FireOutcome
import dev.sard.server.scheduler.FireReason
import java.time.ZoneId
import java.time.format.DateTimeFormatter

private val MOMENT: DateTimeFormatter = DateTimeFormatter.ofPattern("yyyy-MM-dd HH:mm")

/** The words of the alert in one language (F3b, Н3, Н4); names of sources and hosts are never translated. */
private class AlertWords(
    val title: String,
    val agent: String,
    val skipped: String,
    val firedAt: String,
    val activeRun: String,
    val reasons: Map<Pair<FireOutcome, FireReason?>, String>,
    val active: String,
)

private val EN =
    AlertWords(
        title = "⚠️ Scheduled backup is not running",
        agent = "Agent: ",
        skipped = "Skipped in a row: ",
        firedAt = "Skipped fire: ",
        activeRun = "Active run: ",
        active = "Reason: the previous run is still going",
        reasons =
            mapOf(
                (FireOutcome.SKIPPED_GONE to FireReason.AGENT_REVOKED) to "Reason: the source's agent was revoked",
                (FireOutcome.SKIPPED_GONE to FireReason.SOURCE_DELETED) to "Reason: the source was deleted",
                (FireOutcome.REFUSED to FireReason.UNKNOWN_PLUGIN) to
                    "Reason: the agent no longer offers the source's plugin",
                (FireOutcome.REFUSED to FireReason.UNKNOWN_REPOSITORY) to
                    "Reason: the agent no longer reports the source's repository",
            ),
    )

private val RU =
    AlertWords(
        title = "⚠️ Бэкап по расписанию не выполняется",
        agent = "Агент: ",
        skipped = "Пропущено подряд: ",
        firedAt = "Пропущенное срабатывание: ",
        activeRun = "Идущий запуск: ",
        active = "Причина: предыдущий запуск всё ещё идёт",
        reasons =
            mapOf(
                (FireOutcome.SKIPPED_GONE to FireReason.AGENT_REVOKED) to "Причина: агент источника отозван",
                (FireOutcome.SKIPPED_GONE to FireReason.SOURCE_DELETED) to "Причина: источник удалён",
                (FireOutcome.REFUSED to FireReason.UNKNOWN_PLUGIN) to
                    "Причина: агент больше не предлагает плагин источника",
                (FireOutcome.REFUSED to FireReason.UNKNOWN_REPOSITORY) to
                    "Причина: агент больше не сообщает репозиторий источника",
            ),
    )

/** What the alert about fires skipped in a row says (F3b, Н3): the source, the agent's host, the count, the cause. */
class ScheduleAlertFormatter(
    language: NoticeLanguage,
    private val console: ConsoleUrl?,
) : SkipAlertFormatter {
    private val words =
        when (language) {
            NoticeLanguage.EN -> EN
            NoticeLanguage.RU -> RU
        }

    override fun format(notice: SkipAlertNotice): Message {
        val lines =
            listOfNotNull(
                listOf(Message.Bold("${words.title}: ${notice.sourceName}")),
                listOf(Message.Text(words.agent), Message.Code(notice.agentHostname)),
                listOf(Message.Text("${words.skipped}${notice.skippedInRow}")),
                listOf(Message.Text(reason(notice))),
                listOf(Message.Text(words.firedAt), Message.Code(moment(notice))),
                activeRunLine(notice),
                linkLine(notice),
            )
        return Message.ofLines(lines)
    }

    private fun activeRunLine(notice: SkipAlertNotice): List<Message.Part>? {
        val run = notice.activeRunId ?: return null
        return listOf(Message.Text(words.activeRun), Message.Code("$run"))
    }

    private fun linkLine(notice: SkipAlertNotice): List<Message.Part>? {
        val address = console ?: return null
        return listOf(Message.Text(link(address, notice)))
    }

    /** The cause is the outcome of the fire that raised the alert (decision 10). */
    private fun reason(notice: SkipAlertNotice): String =
        if (notice.outcome == FireOutcome.SKIPPED_ACTIVE) {
            words.active
        } else {
            words.reasons.getValue(notice.outcome to notice.reason)
        }

    private fun moment(notice: SkipAlertNotice): String =
        "${MOMENT.format(notice.scheduledFor.atZone(ZoneId.of(notice.timezone)))} ${notice.timezone}"

    private fun link(
        console: ConsoleUrl,
        notice: SkipAlertNotice,
    ): String = notice.activeRunId?.let(console::run) ?: console.source(notice.sourceId)
}
