// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.notify

import dev.sard.server.fleet.CertificateEvent
import java.time.Duration
import java.time.ZoneOffset
import java.time.format.DateTimeFormatter

/** Times in a message: UTC to the minute (as in [OverdueNoticeFormatter]). */
private val MOMENT: DateTimeFormatter = DateTimeFormatter.ofPattern("yyyy-MM-dd HH:mm 'UTC'").withZone(ZoneOffset.UTC)

private const val STATUS_COMMAND = "sudo sard-agent status"
private const val ENROLL_COMMAND = "sudo sard-agent enroll --force"
private const val BUILTIN_LOG_COMMAND = "docker compose logs self-agent"
private val DAY: Duration = Duration.ofDays(1)

/** The words of a certificate notice in one language (C1 phase 3, П38 and П38б); names are never translated. */
private class CertificateWords(
    val expiresSoon: String,
    val expires: String,
    val timeLeft: String,
    val days: String,
    val agentId: String,
    val expired: String,
    val expiredAt: String,
    val issuedUnconfirmed: (String) -> String,
    val offlineSince: (String) -> String,
    val neverConnected: String,
    val onlineNotRenewed: String,
    val onHost: String,
    val renewalError: String,
    val builtinAgent: String,
    val builtinSoon: String,
    val agentSoon: String,
    val agentExpiredLead: String,
    val agentExpiredTail: String,
    val builtinExpired: String,
)

private val EN =
    CertificateWords(
        expiresSoon = "Agent certificate expires soon",
        expires = "Expires: ",
        timeLeft = "Time left: ",
        days = "d",
        agentId = "Agent ID: ",
        expired = "Agent certificate expired",
        expiredAt = "Expired: ",
        issuedUnconfirmed = { "A new certificate was issued $it, but the agent has not confirmed it" },
        offlineSince = { "The agent has been offline since $it" },
        neverConnected = "The agent has never connected",
        onlineNotRenewed = "The agent is online but has not renewed its certificate",
        onHost = "On the agent host: ",
        renewalError = " — the renewal_last_error line names the cause",
        builtinAgent = "Built-in agent: ",
        builtinSoon =
            "If the certificate expires, revoke the built-in agent in the console: it enrolls again by itself",
        agentSoon = "If the certificate expires, the agent cannot connect until it is enrolled again",
        agentExpiredLead = "The agent cannot connect until it is enrolled again: ",
        agentExpiredTail = " with a new token; the agent gets a new ID",
        builtinExpired = "Revoke the built-in agent in the console: it enrolls again by itself",
    )

private val RU =
    CertificateWords(
        expiresSoon = "Сертификат агента скоро истечёт",
        expires = "Истекает: ",
        timeLeft = "Осталось: ",
        days = "сут.",
        agentId = "ID агента: ",
        expired = "Сертификат агента истёк",
        expiredAt = "Истёк: ",
        issuedUnconfirmed = { "Новый сертификат выдан $it, но агент его не подтвердил" },
        offlineSince = { "Агент не на связи с $it" },
        neverConnected = "Агент ни разу не подключался",
        onlineNotRenewed = "Агент на связи, но не продлил сертификат",
        onHost = "На хосте агента: ",
        renewalError = " — строка renewal_last_error называет причину",
        builtinAgent = "Встроенный агент: ",
        builtinSoon = "Если сертификат истечёт, отзовите встроенного агента в консоли — он зарегистрируется заново сам",
        agentSoon = "Если сертификат истечёт, агент не подключится, пока его не зарегистрируют заново",
        agentExpiredLead = "Агент не подключится, пока его не зарегистрируют заново: ",
        agentExpiredTail = " с новым токеном — у агента будет новый ID",
        builtinExpired = "Отзовите встроенного агента в консоли — он зарегистрируется заново сам",
    )

/**
 * What a notice about an agent certificate says (C1 phase 3, П38, П38б): what happened, what the server knows of
 * the renewal, and what to do. A threshold is not named in the text: with a scaled lifetime "30 days" would be false.
 */
class CertificateNoticeFormatter(
    language: NoticeLanguage,
    private val console: ConsoleUrl?,
) : CertificateFormatter {
    private val words =
        when (language) {
            NoticeLanguage.EN -> EN
            NoticeLanguage.RU -> RU
        }
    private val figures = NoticeFigures(wording(language))

    override fun format(notice: CertificateNotice): Message {
        val lines =
            when (notice.event) {
                CertificateEvent.EXPIRED -> expired(notice)

                CertificateEvent.THRESHOLD_30,
                CertificateEvent.THRESHOLD_7,
                CertificateEvent.THRESHOLD_1,
                -> soon(notice)
            }
        return Message.ofLines(lines + listOfNotNull(console?.let { listOf(Message.Text(it.agent(notice.agentId))) }))
    }

    private fun soon(notice: CertificateNotice): List<List<Message.Part>> =
        listOf(
            listOf(Message.Bold("⚠️ ${words.expiresSoon}: ${notice.hostname}")),
            listOf(Message.Text(words.expires + MOMENT.format(notice.notAfter))),
            listOf(Message.Text(words.timeLeft + timeLeft(notice))),
            agentId(notice),
            knows(notice),
        ) + soonAdvice(notice)

    private fun expired(notice: CertificateNotice): List<List<Message.Part>> =
        listOf(
            listOf(Message.Bold("⛔ ${words.expired}: ${notice.hostname}")),
            listOf(Message.Text(words.expiredAt + MOMENT.format(notice.notAfter))),
            agentId(notice),
            knows(notice),
            if (notice.builtin) {
                listOf(Message.Text(words.builtinExpired))
            } else {
                listOf(
                    Message.Text(words.agentExpiredLead),
                    Message.Code(ENROLL_COMMAND),
                    Message.Text(words.agentExpiredTail),
                )
            },
        )

    private fun soonAdvice(notice: CertificateNotice): List<List<Message.Part>> =
        if (notice.builtin) {
            listOf(
                listOf(Message.Text(words.builtinAgent), Message.Code(BUILTIN_LOG_COMMAND)),
                listOf(Message.Text(words.builtinSoon)),
            )
        } else {
            listOf(
                listOf(Message.Text(words.onHost), Message.Code(STATUS_COMMAND), Message.Text(words.renewalError)),
                listOf(Message.Text(words.agentSoon)),
            )
        }

    private fun agentId(notice: CertificateNotice): List<Message.Part> {
        val id = Message.Code("${notice.agentId}")
        return listOf(Message.Text(words.agentId), id)
    }

    /** More than a day: whole days rounded up; up to a day: a duration as the run messages write it. */
    private fun timeLeft(notice: CertificateNotice): String {
        val left = Duration.between(notice.now, notice.notAfter)
        return if (left > DAY) "${ceilingDays(left)} ${words.days}" else figures.duration(left)
    }

    private fun ceilingDays(left: Duration): Long {
        val whole = left.toDays()
        return if (left > Duration.ofDays(whole)) whole + 1 else whole
    }

    /** What the server sees, the first that fits (П38, line 5). */
    private fun knows(notice: CertificateNotice): List<Message.Part> {
        val text =
            when {
                notice.renewalIssuedAt != null -> words.issuedUnconfirmed(MOMENT.format(notice.renewalIssuedAt))
                notice.online -> words.onlineNotRenewed
                notice.lastSeenAt != null -> words.offlineSince(MOMENT.format(notice.lastSeenAt))
                else -> words.neverConnected
            }
        return listOf(Message.Text(text))
    }
}
