// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.notify

import dev.sard.server.scheduler.FireOutcome
import dev.sard.server.scheduler.FireReason
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import java.time.Instant
import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

private val SOURCE: UUID = UUID.fromString("0192f0c4-7a10-7000-8000-00000000b001")
private val ACTIVE: UUID = UUID.fromString("0192f0c4-7a10-7000-8000-00000000a002")
private const val CONSOLE = "https://sard.example.com"

private fun alert(
    outcome: FireOutcome = FireOutcome.SKIPPED_ACTIVE,
    reason: FireReason? = null,
    zone: String = "Europe/Berlin",
    at: String = "2026-10-10T00:00:00Z",
) = SkipAlertNotice(
    tenantId = UUID.randomUUID(),
    fireId = UUID.randomUUID(),
    sourceId = SOURCE,
    sourceName = "db-main",
    agentHostname = "db1.example.com",
    skippedInRow = 3,
    outcome = outcome,
    reason = reason,
    scheduledFor = Instant.parse(at),
    timezone = zone,
    activeRunId = ACTIVE.takeIf { outcome == FireOutcome.SKIPPED_ACTIVE },
)

/** F3b: the alert about fires skipped in a row, in both languages (Н3, Н4). */
@MutFlowTest
class SkipAlertFormatterTest {
    private fun format(
        notice: SkipAlertNotice,
        language: NoticeLanguage = NoticeLanguage.RU,
        console: String = CONSOLE,
    ): Message {
        val formatter = ScheduleAlertFormatter(language, ConsoleUrl.parse(console))
        return MutFlow.underTest { formatter.format(notice) }
    }

    @Test
    fun `Полный текст алерта о пропусках из-за идущего запуска на русском`() {
        val expected =
            """
            ⚠️ Бэкап по расписанию не выполняется: db-main
            Агент: db1.example.com
            Пропущено подряд: 3
            Причина: предыдущий запуск всё ещё идёт
            Пропущенное срабатывание: 2026-10-10 02:00 Europe/Berlin
            Идущий запуск: $ACTIVE
            $CONSOLE/runs/$ACTIVE
            """.trimIndent()
        assertEquals(expected, format(alert()).plainText())
    }

    @Test
    fun `Полный текст алерта о пропусках на английском`() {
        val expected =
            """
            ⚠️ Scheduled backup is not running: db-main
            Agent: db1.example.com
            Skipped in a row: 3
            Reason: the previous run is still going
            Skipped fire: 2026-10-10 02:00 Europe/Berlin
            Active run: $ACTIVE
            $CONSOLE/runs/$ACTIVE
            """.trimIndent()
        assertEquals(expected, format(alert(), NoticeLanguage.EN).plainText())
    }

    @Test
    fun `Первая строка выделена жирным, хост, время и идущий запуск моноширинные`() {
        val message = format(alert())

        assertEquals(Message.Bold("⚠️ Бэкап по расписанию не выполняется: db-main"), message.parts.first())
        val code = message.parts.filterIsInstance<Message.Code>().map { it.text }
        assertEquals(listOf("db1.example.com", "2026-10-10 02:00 Europe/Berlin", "$ACTIVE"), code)
        assertEquals(1, message.parts.filterIsInstance<Message.Bold>().size)
    }

    @Test
    fun `Причина называет исход пропуска, поднявшего алерт, а ссылка ведёт на источник`() {
        val reasons =
            listOf(
                Triple(FireOutcome.SKIPPED_GONE, FireReason.AGENT_REVOKED, "Причина: агент источника отозван"),
                Triple(FireOutcome.SKIPPED_GONE, FireReason.SOURCE_DELETED, "Причина: источник удалён"),
                Triple(
                    FireOutcome.REFUSED,
                    FireReason.UNKNOWN_PLUGIN,
                    "Причина: агент больше не предлагает плагин источника",
                ),
                Triple(
                    FireOutcome.REFUSED,
                    FireReason.UNKNOWN_REPOSITORY,
                    "Причина: агент больше не сообщает репозиторий источника",
                ),
            )
        for ((outcome, reason, line) in reasons) {
            val lines = format(alert(outcome, reason)).plainText().lines()
            assertEquals(line, lines[3], "$reason")
            assertTrue(lines.none { it.startsWith("Идущий запуск") })
            assertEquals("$CONSOLE/sources/$SOURCE", lines.last())
        }
    }

    @Test
    fun `Причины на английском`() {
        val reasons =
            mapOf(
                FireReason.AGENT_REVOKED to "Reason: the source's agent was revoked",
                FireReason.SOURCE_DELETED to "Reason: the source was deleted",
                FireReason.UNKNOWN_PLUGIN to "Reason: the agent no longer offers the source's plugin",
                FireReason.UNKNOWN_REPOSITORY to "Reason: the agent no longer reports the source's repository",
            )
        for ((reason, line) in reasons) {
            val outcome = if (reason.stored.startsWith("unknown")) FireOutcome.REFUSED else FireOutcome.SKIPPED_GONE
            assertEquals(line, format(alert(outcome, reason), NoticeLanguage.EN).plainText().lines()[3], "$reason")
        }
    }

    @Test
    fun `Без публичного адреса консоли алерт уходит без ссылки`() {
        val text = format(alert(), console = "").plainText()

        assertEquals("Идущий запуск: $ACTIVE", text.lines().last())
        assertTrue("http" !in text)
    }

    @Test
    fun `Время пропущенного срабатывания записано в поясе расписания`() {
        val text = format(alert(zone = "America/New_York", at = "2026-10-10T06:00:00Z")).plainText()

        assertEquals("Пропущенное срабатывание: 2026-10-10 02:00 America/New_York", text.lines()[4])
    }
}
