// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.notify

import java.time.ZoneOffset
import java.time.format.DateTimeFormatter

/** Times in a message: UTC to the minute, the same for every reader of a shared chat. */
private val MOMENT: DateTimeFormatter = DateTimeFormatter.ofPattern("yyyy-MM-dd HH:mm 'UTC'").withZone(ZoneOffset.UTC)

/** The words of an overdue episode in one language (F6); names of sources and hosts are never translated. */
private class OverdueWords(
    val opened: String,
    val resumed: String,
    val agent: String,
    val run: String,
    val lastSuccess: String,
    val never: String,
    val expectedBy: String,
)

private val EN =
    OverdueWords(
        opened = "No successful backup for too long",
        resumed = "Backups are back",
        agent = "Agent: ",
        run = "Run: ",
        lastSuccess = "Last success: ",
        never = "There has been no successful backup yet",
        expectedBy = "Expected by: ",
    )

private val RU =
    OverdueWords(
        opened = "Давно не было успешного бэкапа",
        resumed = "Бэкапы возобновились",
        agent = "Агент: ",
        run = "Запуск: ",
        lastSuccess = "Последний успех: ",
        never = "Успешных бэкапов ещё не было",
        expectedBy = "Ожидался не позже: ",
    )

/**
 * What an overdue episode says (F6, ADR 00XX-draft-source-freshness): it opened, with the last success and the
 * deadline; or a success closed it, unless that run is told about itself (a manual result, a recovery): one
 * message per turn of events.
 */
class OverdueNoticeFormatter(
    language: NoticeLanguage,
    private val console: ConsoleUrl?,
) : OverdueFormatter {
    private val words =
        when (language) {
            NoticeLanguage.EN -> EN
            NoticeLanguage.RU -> RU
        }

    override fun format(notice: OverdueNotice): Message? {
        val closing = notice.closedBy ?: return Message.ofLines(opened(notice))
        return if (Telling.of(closing) == null) Message.ofLines(resumed(notice, closing)) else null
    }

    private fun opened(notice: OverdueNotice): List<List<Message.Part>> {
        val last = notice.countedFrom?.let { Message.Text(words.lastSuccess + MOMENT.format(it)) }
        return listOf(
            listOf(Message.Bold("⚠️ ${words.opened}: ${notice.sourceName}")),
            listOf(Message.Text(words.agent), Message.Code(notice.agentHostname)),
            listOf(last ?: Message.Text(words.never)),
            listOf(Message.Text(words.expectedBy + MOMENT.format(notice.deadline))),
        )
    }

    private fun resumed(
        notice: OverdueNotice,
        closing: RunNotice,
    ): List<List<Message.Part>> =
        listOfNotNull(
            listOf(Message.Bold("✅ ${words.resumed}: ${notice.sourceName}")),
            listOf(Message.Text(words.agent), Message.Code(notice.agentHostname)),
            listOf(Message.Text(words.run), Message.Code("${closing.runId}")),
            console?.let { listOf(Message.Text(it.run(closing.runId))) },
        )
}
