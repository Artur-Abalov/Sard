// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.notify

import dev.sard.server.fleet.CertificateEvent
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import java.time.Instant
import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertIs
import kotlin.test.assertTrue

private val AGENT: UUID = UUID.fromString("0192f0c4-7a10-7000-8000-00000000d001")
private val NOT_AFTER: Instant = Instant.parse("2027-10-15T12:00:00Z")
private const val CONSOLE = "https://sard.example.com"
private val THRESHOLDS =
    listOf(CertificateEvent.THRESHOLD_30, CertificateEvent.THRESHOLD_7, CertificateEvent.THRESHOLD_1)
private val HEARTBEAT: Instant = Instant.parse("2027-09-01T08:30:00Z")

private fun notice(
    event: CertificateEvent = CertificateEvent.THRESHOLD_30,
    now: Instant = Instant.parse("2027-09-15T12:00:00Z"),
    hostname: String = "db1.example.com",
    builtin: Boolean = false,
    online: Boolean = true,
    lastSeenAt: Instant? = null,
    renewalIssuedAt: Instant? = null,
) = CertificateNotice(
    tenantId = UUID.randomUUID(),
    agentId = AGENT,
    hostname = hostname,
    builtin = builtin,
    event = event,
    notAfter = NOT_AFTER,
    now = now,
    renewalIssuedAt = renewalIssuedAt,
    online = online,
    lastSeenAt = lastSeenAt,
)

private fun Message.text() = parts.joinToString("") { it.text }

private fun Message.lines() = text().lines()

private fun formatter(
    language: NoticeLanguage = NoticeLanguage.RU,
    console: String? = CONSOLE,
) = CertificateNoticeFormatter(language, console?.let { ConsoleUrl.parse(it) })

private fun CertificateNoticeFormatter.say(notice: CertificateNotice) = MutFlow.underTest { format(notice) }

/** C1 phase 3, П38 and П38б (S9b): what a notice about an agent certificate says, in both languages. */
@MutFlowTest(includeTargets = [CertificateNoticeFormatter::class])
class CertificateNoticeFormatterTest {
    @Test
    fun `Полный текст уведомления о сертификате на русском`() {
        val expected =
            """
            ⚠️ Сертификат агента скоро истечёт: db1.example.com
            Истекает: 2027-10-15 12:00 UTC
            Осталось: 30 сут.
            ID агента: 0192f0c4-7a10-7000-8000-00000000d001
            Агент на связи, но не продлил сертификат
            На хосте агента: sudo sard-agent status — строка renewal_last_error называет причину
            Если сертификат истечёт, агент не подключится, пока его не зарегистрируют заново
            https://sard.example.com/agents/0192f0c4-7a10-7000-8000-00000000d001
            """.trimIndent()
        assertEquals(expected, formatter().say(notice()).text())
    }

    @Test
    fun `Полный текст уведомления о сертификате на английском`() {
        val expected =
            """
            ⚠️ Agent certificate expires soon: db1.example.com
            Expires: 2027-10-15 12:00 UTC
            Time left: 30 d
            Agent ID: 0192f0c4-7a10-7000-8000-00000000d001
            The agent is online but has not renewed its certificate
            On the agent host: sudo sard-agent status — the renewal_last_error line names the cause
            If the certificate expires, the agent cannot connect until it is enrolled again
            https://sard.example.com/agents/0192f0c4-7a10-7000-8000-00000000d001
            """.trimIndent()
        assertEquals(expected, formatter(NoticeLanguage.EN).say(notice()).text())
    }

    @Test
    fun `Первая строка выделена жирным, идентификатор агента и команда моноширинные`() {
        val message = formatter().say(notice())
        assertIs<Message.Bold>(message.parts.first())
        assertEquals("⚠️ Сертификат агента скоро истечёт: db1.example.com", message.parts.first().text)
        val code = message.parts.filterIsInstance<Message.Code>().map { it.text }
        assertEquals(listOf("$AGENT", "sudo sard-agent status"), code)
        assertEquals(1, message.parts.count { it is Message.Bold })
    }

    @Test
    fun `Пятая строка называет то, что сервер знает о продлении`() {
        val issued = Instant.parse("2027-08-11T12:00:00Z")
        val cases =
            listOf(
                Triple(
                    NoticeLanguage.RU,
                    notice(renewalIssuedAt = issued),
                    "Новый сертификат выдан 2027-08-11 12:00 UTC, но агент его не подтвердил",
                ),
                Triple(
                    NoticeLanguage.RU,
                    notice(online = false, lastSeenAt = HEARTBEAT),
                    "Агент не на связи с 2027-09-01 08:30 UTC",
                ),
                Triple(NoticeLanguage.RU, notice(online = false), "Агент ни разу не подключался"),
                Triple(NoticeLanguage.RU, notice(), "Агент на связи, но не продлил сертификат"),
                Triple(
                    NoticeLanguage.EN,
                    notice(online = false, renewalIssuedAt = issued),
                    "A new certificate was issued 2027-08-11 12:00 UTC, but the agent has not confirmed it",
                ),
                Triple(
                    NoticeLanguage.EN,
                    notice(online = false, lastSeenAt = HEARTBEAT),
                    "The agent has been offline since 2027-09-01 08:30 UTC",
                ),
                Triple(NoticeLanguage.EN, notice(online = false), "The agent has never connected"),
                Triple(NoticeLanguage.EN, notice(), "The agent is online but has not renewed its certificate"),
            )
        for ((language, notice, line) in cases) {
            assertEquals(line, formatter(language).say(notice).lines()[4], "$language $notice")
        }
    }

    @Test
    fun `Остаток назван сутками вверх, а меньше суток — длительностью`() {
        val cases =
            mapOf(
                "2027-09-15T12:00:00Z" to "Осталось: 30 сут.",
                "2027-09-15T12:20:00Z" to "Осталось: 30 сут.",
                "2027-10-08T12:00:00Z" to "Осталось: 7 сут.",
                "2027-10-14T11:59:59Z" to "Осталось: 2 сут.",
                "2027-10-14T12:00:00Z" to "Осталось: 24 ч 0 мин",
                "2027-10-15T11:59:10Z" to "Осталось: 50 с",
            )
        for ((moment, line) in cases) {
            val message = formatter().say(notice(now = Instant.parse(moment)))
            assertEquals(line, message.lines()[2], moment)
        }
    }

    @Test
    fun `Остаток меньше часа называет минуты и секунды`() {
        val message = formatter(NoticeLanguage.EN).say(notice(now = Instant.parse("2027-10-15T11:20:30Z")))
        assertEquals("Time left: 39 min 30 s", message.lines()[2])
    }

    @Test
    fun `Уведомление о встроенном агенте советует смотреть журнал контейнера и отзыв`() {
        val text = formatter().say(notice(hostname = "sard-self", builtin = true)).lines()
        assertEquals("Встроенный агент: docker compose logs self-agent", text[5])
        assertEquals(
            "Если сертификат истечёт, отзовите встроенного агента в консоли — он зарегистрируется заново сам",
            text[6],
        )
        assertFalse(text.any { "sudo sard-agent" in it })
    }

    @Test
    fun `Уведомление о встроенном агенте на английском`() {
        val message = formatter(NoticeLanguage.EN).say(notice(CertificateEvent.THRESHOLD_7, builtin = true))
        assertEquals("Built-in agent: docker compose logs self-agent", message.lines()[5])
        assertEquals(
            "If the certificate expires, revoke the built-in agent in the console: it enrolls again by itself",
            message.lines()[6],
        )
    }

    @Test
    fun `Без публичного адреса консоли уведомление уходит без ссылки`() {
        val message = formatter(console = null).say(notice())
        assertEquals(7, message.lines().size)
        assertFalse("http" in message.text())
    }

    @Test
    fun `Текст не называет номер порога`() {
        val banned = listOf("30 дней", "7 дней", "1 день", "30 days", "7 days", "1 day")
        for (language in NoticeLanguage.entries) {
            for (event in THRESHOLDS) {
                val text = formatter(language).say(notice(event, now = Instant.parse("2027-10-15T11:50:00Z"))).text()
                assertTrue(banned.none { it in text }, text)
            }
        }
    }

    @Test
    fun `Полный текст уведомления об истечении на русском`() {
        val expired = notice(CertificateEvent.EXPIRED, online = false, lastSeenAt = HEARTBEAT)
        val expected =
            """
            ⛔ Сертификат агента истёк: db1.example.com
            Истёк: 2027-10-15 12:00 UTC
            ID агента: 0192f0c4-7a10-7000-8000-00000000d001
            Агент не на связи с 2027-09-01 08:30 UTC
            Агент не подключится, пока его не зарегистрируют заново: sudo sard-agent enroll --force с новым токеном — у агента будет новый ID
            https://sard.example.com/agents/0192f0c4-7a10-7000-8000-00000000d001
            """.trimIndent()
        assertEquals(expected, formatter().say(expired).text())
    }

    @Test
    fun `Полный текст уведомления об истечении на английском`() {
        val expired = notice(CertificateEvent.EXPIRED, online = false, lastSeenAt = HEARTBEAT)
        val expected =
            """
            ⛔ Agent certificate expired: db1.example.com
            Expired: 2027-10-15 12:00 UTC
            Agent ID: 0192f0c4-7a10-7000-8000-00000000d001
            The agent has been offline since 2027-09-01 08:30 UTC
            The agent cannot connect until it is enrolled again: sudo sard-agent enroll --force with a new token; the agent gets a new ID
            https://sard.example.com/agents/0192f0c4-7a10-7000-8000-00000000d001
            """.trimIndent()
        assertEquals(expected, formatter(NoticeLanguage.EN).say(expired).text())
    }

    @Test
    fun `Первая строка уведомления об истечении жирная, команда моноширинная`() {
        val message = formatter().say(notice(CertificateEvent.EXPIRED))
        assertIs<Message.Bold>(message.parts.first())
        assertEquals(1, message.parts.count { it is Message.Bold })
        val code = message.parts.filterIsInstance<Message.Code>().map { it.text }
        assertEquals(listOf("$AGENT", "sudo sard-agent enroll --force"), code)
    }

    @Test
    fun `Уведомление об истечении называет выданный, но не подтверждённый сертификат`() {
        val issued = Instant.parse("2027-10-10T09:00:00Z")
        val message = formatter().say(notice(CertificateEvent.EXPIRED, renewalIssuedAt = issued))
        assertEquals("Новый сертификат выдан 2027-10-10 09:00 UTC, но агент его не подтвердил", message.lines()[3])
    }

    @Test
    fun `Уведомление об истечении встроенного агента советует отозвать его`() {
        val cases =
            mapOf(
                NoticeLanguage.RU to "Отзовите встроенного агента в консоли — он зарегистрируется заново сам",
                NoticeLanguage.EN to "Revoke the built-in agent in the console: it enrolls again by itself",
            )
        for ((language, line) in cases) {
            val builtin = notice(CertificateEvent.EXPIRED, hostname = "sard-self", builtin = true)
            val message = formatter(language).say(builtin)
            assertEquals(line, message.lines()[4], "$language")
            assertFalse("sudo sard-agent" in message.text())
        }
    }

    @Test
    fun `Без публичного адреса консоли уведомление об истечении уходит без ссылки`() {
        val message = formatter(console = null).say(notice(CertificateEvent.EXPIRED))
        assertEquals(5, message.lines().size)
        assertFalse("http" in message.text())
    }
}
