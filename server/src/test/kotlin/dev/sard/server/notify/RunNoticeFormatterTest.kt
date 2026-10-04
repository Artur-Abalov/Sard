// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.notify

import dev.sard.server.runs.RunState
import dev.sard.server.runs.StepState
import dev.sard.server.runs.Trigger
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import java.time.Duration
import java.time.Instant
import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNotNull
import kotlin.test.assertNull
import kotlin.test.assertTrue

private val RUN: UUID = UUID.fromString("0192f0c4-7a10-7000-8000-00000000a001")
private val T0: Instant = Instant.parse("2026-10-04T10:00:00Z")
private const val CONSOLE = "https://sard.example.com"
private val GIB = BackupSizes(totalBytes = 1_610_612_736, addedBytes = 12_897_485)
private val LINK = "$CONSOLE/runs/$RUN"

private fun notice(
    step: StepState = StepState.SUCCEEDED,
    message: String? = null,
    source: String = "db-main",
    host: String = "db1.example.com",
    startedAt: Instant? = T0.plusSeconds(5),
    finishedAt: Instant = T0.plusSeconds(155),
    backup: BackupSizes? = if (step == StepState.SUCCEEDED) GIB else null,
    trigger: Trigger = Trigger.MANUAL,
) = RunNotice(
    tenantId = UUID.randomUUID(),
    runId = RUN,
    trigger = trigger,
    status = RunState.following(step),
    message = message,
    queuedAt = T0,
    finishedAt = finishedAt,
    sourceId = UUID.randomUUID(),
    sourceName = source,
    agentId = UUID.randomUUID(),
    agentHostname = host,
    stepStatus = step,
    startedAt = startedAt,
    backup = backup,
)

private fun Message.text() = parts.joinToString("") { it.text }

private fun Message.lines() = text().split("\n")

/** S9b: the wording of a run notification, in both languages (the specification's templates). */
@MutFlowTest
class RunNoticeFormatterTest {
    private fun format(
        notice: RunNotice,
        language: NoticeLanguage = NoticeLanguage.RU,
        console: String = CONSOLE,
    ): Message {
        val formatter = RunNoticeFormatter(language, ConsoleUrl.parse(console))
        return assertNotNull(MutFlow.underTest { formatter.format(notice) })
    }

    // --- full texts

    @Test
    fun `Полный текст сообщения об успехе на русском`() {
        val expected =
            """
            ✅ Бэкап выполнен: db-main
            Агент: db1.example.com
            Длительность: 2 мин 30 с
            Всего: 1,5 ГиБ, добавлено: 12,3 МиБ
            Запуск: $RUN
            $LINK
            """.trimIndent()
        assertEquals(expected, format(notice()).text())
    }

    @Test
    fun `Полный текст сообщения об успехе на английском`() {
        val expected =
            """
            ✅ Backup succeeded: db-main
            Agent: db1.example.com
            Duration: 2 min 30 s
            Total: 1.5 GiB, added: 12.3 MiB
            Run: $RUN
            $LINK
            """.trimIndent()
        assertEquals(expected, format(notice(), NoticeLanguage.EN).text())
    }

    @Test
    fun `Первая строка выделена жирным, хост и идентификатор запуска моноширинные`() {
        val message = format(notice())

        assertEquals(Message.Bold("✅ Бэкап выполнен: db-main"), message.parts.first())
        val code = message.parts.filterIsInstance<Message.Code>().map { it.text }
        assertEquals(listOf("db1.example.com", "$RUN"), code)
    }

    @Test
    fun `Запуск не вручную не уведомляется`() {
        val formatter = RunNoticeFormatter(NoticeLanguage.RU, null)
        for (trigger in listOf(Trigger.SCHEDULE, Trigger.VERIFICATION)) {
            val finished = notice(trigger = trigger)
            assertNull(MutFlow.underTest { formatter.format(finished) })
        }
    }

    // --- sizes

    private fun sizeLine(
        bytes: Long,
        language: NoticeLanguage,
    ): String {
        val lines = format(notice(backup = BackupSizes(bytes, 0)), language).lines()
        return lines.single { it.startsWith("Всего") || it.startsWith("Total") }
    }

    @Test
    fun `Размер пишется в двоичных единицах с одним знаком`() {
        val cases =
            listOf(
                Triple(0L, NoticeLanguage.RU, "Всего: 0 Б,"),
                Triple(1023L, NoticeLanguage.RU, "Всего: 1023 Б,"),
                Triple(1024L, NoticeLanguage.RU, "Всего: 1 КиБ,"),
                Triple(1280L, NoticeLanguage.RU, "Всего: 1,3 КиБ,"),
                Triple(1536L, NoticeLanguage.EN, "Total: 1.5 KiB,"),
                Triple(1_048_575L, NoticeLanguage.RU, "Всего: 1024 КиБ,"),
                Triple(1_610_612_736L, NoticeLanguage.RU, "Всего: 1,5 ГиБ,"),
                Triple(5_629_499_534_213_120L, NoticeLanguage.EN, "Total: 5120 TiB,"),
            )
        for ((bytes, language, start) in cases) {
            val line = sizeLine(bytes, language)
            assertTrue(line.startsWith(start), "$bytes $language: $line")
        }
    }

    // --- duration

    @Test
    fun `Длительность пишется как на странице запуска`() {
        val cases =
            listOf(
                Triple(0L, NoticeLanguage.RU, "Длительность: 0 с"),
                Triple(59_999L, NoticeLanguage.RU, "Длительность: 59 с"),
                Triple(60_000L, NoticeLanguage.RU, "Длительность: 1 мин 0 с"),
                Triple(3_599_000L, NoticeLanguage.EN, "Duration: 59 min 59 s"),
                Triple(3_600_000L, NoticeLanguage.RU, "Длительность: 1 ч 0 мин"),
                Triple(90_061_000L, NoticeLanguage.EN, "Duration: 25 h 1 min"),
            )
        for ((millis, language, expected) in cases) {
            val message = format(notice(startedAt = T0, finishedAt = T0.plusMillis(millis)), language)
            assertEquals(expected, message.lines()[2])
        }
    }

    // --- headlines

    @Test
    fun `Заголовок называет итог по статусу шага`() {
        val cases =
            listOf(
                Triple(StepState.SUCCEEDED, NoticeLanguage.RU, "✅ Бэкап выполнен: db-main"),
                Triple(StepState.FAILED, NoticeLanguage.RU, "❌ Бэкап завершился ошибкой: db-main"),
                Triple(StepState.REJECTED, NoticeLanguage.RU, "❌ Бэкап отклонён агентом: db-main"),
                Triple(StepState.LOST, NoticeLanguage.RU, "❌ Бэкап потерян: db-main"),
                Triple(StepState.TIMED_OUT, NoticeLanguage.RU, "❌ Бэкап прерван по таймауту: db-main"),
                Triple(StepState.CANCELLED, NoticeLanguage.RU, "⏹ Бэкап отменён: db-main"),
                Triple(StepState.SUCCEEDED, NoticeLanguage.EN, "✅ Backup succeeded: db-main"),
                Triple(StepState.FAILED, NoticeLanguage.EN, "❌ Backup failed: db-main"),
                Triple(StepState.REJECTED, NoticeLanguage.EN, "❌ Backup rejected by the agent: db-main"),
                Triple(StepState.LOST, NoticeLanguage.EN, "❌ Backup lost: db-main"),
                Triple(StepState.TIMED_OUT, NoticeLanguage.EN, "❌ Backup timed out: db-main"),
                Triple(StepState.CANCELLED, NoticeLanguage.EN, "⏹ Backup cancelled: db-main"),
            )
        for ((step, language, expected) in cases) {
            assertEquals(expected, format(notice(step), language).lines().first(), "$step $language")
        }
    }

    @Test
    fun `Без значка заголовки успеха и ошибки всё равно различаются`() {
        for (language in NoticeLanguage.entries) {
            val words =
                StepState.entries
                    .filterNot { it.active }
                    .map { format(notice(it, source = "x"), language).lines().first().substringAfter(' ') }
            assertEquals(words.size, words.toSet().size, "$language: $words")
        }
    }

    @Test
    fun `У каждого завершённого состояния шага свой заголовок на каждом языке`() {
        for (language in NoticeLanguage.entries) {
            val headlines =
                StepState.entries.filterNot { it.active }.map { step ->
                    format(notice(step, source = "x"), language).lines().first()
                }
            assertEquals(headlines.size, headlines.toSet().size, "$language: $headlines")
            assertTrue(headlines.all { it.endsWith(": x") }, "$language: $headlines")
        }
    }

    @Test
    fun `Активное состояние шага не уведомляется ни на одном языке`() {
        for (language in NoticeLanguage.entries) {
            val formatter = RunNoticeFormatter(language, null)
            for (step in StepState.entries.filter { it.active }) {
                val active = notice(step)
                assertNull(MutFlow.underTest { formatter.format(active) }, "$language $step")
            }
        }
    }

    @Test
    fun `Имена источника и агента не переводятся`() {
        val message = format(notice(source = "Бэкап базы"), NoticeLanguage.EN)
        assertEquals("✅ Backup succeeded: Бэкап базы", message.lines().first())
    }

    @Test
    fun `Идентификатор запуска есть и без ссылки`() {
        val message = format(notice(StepState.LOST), console = "")
        assertEquals("Запуск: $RUN", message.lines().last())
        assertTrue("http" !in message.text())
    }

    @Test
    fun `Бездействующий шаг не уведомляется`() {
        val formatter = RunNoticeFormatter(NoticeLanguage.RU, null)
        val running = notice(StepState.RUNNING)
        assertNull(MutFlow.underTest { formatter.format(running) })
    }

    @Test
    fun `Длительность между началом и концом не отрицательна`() {
        val message = format(notice(startedAt = T0.plus(Duration.ofSeconds(10)), finishedAt = T0))
        assertEquals("Длительность: 0 с", message.lines()[2])
    }

    // --- the reason

    @Test
    fun `Полный текст сообщения об ошибке на русском`() {
        val expected =
            """
            ❌ Бэкап завершился ошибкой: db-main
            Агент: db1.example.com
            Длительность: 2 мин 30 с
            Причина: restic exited with code 1
            Запуск: $RUN
            $LINK
            """.trimIndent()
        assertEquals(expected, format(notice(StepState.FAILED, "restic exited with code 1")).text())
    }

    @Test
    fun `Полный текст сообщения об ошибке на английском`() {
        val expected =
            """
            ❌ Backup failed: db-main
            Agent: db1.example.com
            Duration: 2 min 30 s
            Reason: restic exited with code 1
            Run: $RUN
            $LINK
            """.trimIndent()
        assertEquals(expected, format(notice(StepState.FAILED, "restic exited with code 1"), NoticeLanguage.EN).text())
    }

    @Test
    fun `Причина пишется дословно вместе с переводами строк`() {
        val reason = "open /data/a: permission denied\nopen /data/b: permission denied"
        val message = format(notice(StepState.FAILED, reason))
        assertTrue(Message.Code(reason) in message.parts)
    }

    @Test
    fun `Пустая причина названа отсутствующей`() {
        for (reason in listOf(null, "")) {
            assertEquals("Причина: не указана", format(notice(StepState.FAILED, reason)).lines()[3])
        }
    }

    @Test
    fun `Причина из одних пробелов названа отсутствующей`() {
        val message = format(notice(StepState.FAILED, "   "), NoticeLanguage.EN)
        assertEquals("Reason: not given", message.lines()[3])
    }

    @Test
    fun `Отклонённый агентом шаг без начала выполнения не имеет строки длительности`() {
        val rejected = notice(StepState.REJECTED, "unknown plugin \"absent\"", startedAt = null)
        val expected =
            """
            ❌ Бэкап отклонён агентом: db-main
            Агент: db1.example.com
            Причина: unknown plugin "absent"
            Запуск: $RUN
            $LINK
            """.trimIndent()
        assertEquals(expected, format(rejected).text())
    }

    // --- a snapshot that was made

    @Test
    fun `Ошибка с сохранённым снимком сообщает о снимке`() {
        val failed = notice(StepState.FAILED, "11 files could not be read", backup = GIB)
        val expected =
            """
            ❌ Бэкап завершился ошибкой: db-main
            Агент: db1.example.com
            Длительность: 2 мин 30 с
            Причина: 11 files could not be read
            Снимок создан и пригоден для восстановления, но часть данных в него не попала.
            Всего: 1,5 ГиБ, добавлено: 12,3 МиБ
            Запуск: $RUN
            $LINK
            """.trimIndent()
        assertEquals(expected, format(failed).text())
    }

    @Test
    fun `Ошибка с сохранённым снимком на английском`() {
        val message = format(notice(StepState.FAILED, "boom", backup = GIB), NoticeLanguage.EN)
        val note = "The snapshot was created and can be restored, but part of the data did not get into it."
        assertEquals(listOf("Reason: boom", note), message.lines().subList(3, 5))
    }

    @Test
    fun `Ошибка без снимка не упоминает снимок и размеры`() {
        val text = format(notice(StepState.FAILED, "boom", backup = null)).text()
        assertTrue("Снимок" !in text)
        assertTrue("Всего" !in text)
    }

    // --- a lost step

    @Test
    fun `Полный текст сообщения о потерянном шаге на русском`() {
        val expected =
            """
            ❌ Бэкап потерян: db-main
            Агент: db1.example.com
            Длительность: 2 мин 30 с
            Причина: agent lost the step
            Связь с агентом прервалась, пока шаг выполнялся. Можно запустить бэкап снова.
            Запуск: $RUN
            $LINK
            """.trimIndent()
        assertEquals(expected, format(notice(StepState.LOST, "agent lost the step")).text())
    }

    @Test
    fun `Пояснение к потерянному шагу на английском`() {
        val message = format(notice(StepState.LOST, "agent lost the step"), NoticeLanguage.EN)
        val note =
            "The connection to the agent was interrupted while the step was running. " +
                "The next run can be started."
        assertEquals(note, message.lines()[4])
    }

    @Test
    fun `Пояснение о связи есть только у потерянного шага`() {
        val text = format(notice(StepState.FAILED, "connection reset")).text()
        assertTrue("Связь с агентом прервалась" !in text)
    }

    @Test
    fun `Потерянный шаг с поздним выводом бэкапа сообщается так же, как без него`() {
        val without = format(notice(StepState.LOST, "agent lost the step", backup = null)).text()
        val late = format(notice(StepState.LOST, "agent lost the step", backup = GIB)).text()
        assertEquals(without, late)
    }

    // --- a long reason

    @Test
    fun `Причина ровно 500 символов не обрезается`() {
        val message = format(notice(StepState.FAILED, "x".repeat(500)))
        assertTrue(Message.Code("x".repeat(500)) in message.parts)
        assertTrue("Полный текст — в консоли." !in message.text())
    }

    @Test
    fun `Причина 501 символ обрезается до 500 с меткой и строкой о консоли`() {
        val message = format(notice(StepState.FAILED, "x".repeat(501)))
        assertTrue(Message.Code("x".repeat(500) + "…") in message.parts)
        val lines = message.lines()
        assertEquals("Полный текст — в консоли.", lines[lines.indexOfFirst { it.startsWith("Причина") } + 1])
    }

    @Test
    fun `Строка о консоли на английском`() {
        val message = format(notice(StepState.FAILED, "x".repeat(501)), NoticeLanguage.EN)
        assertEquals("The full text is in the console.", message.lines()[4])
    }

    @Test
    fun `Обрезка считает символы, а не байты и не половинки суррогатных пар`() {
        val message = format(notice(StepState.FAILED, "x".repeat(499) + "📦📦"))
        assertTrue(Message.Code("x".repeat(499) + "📦…") in message.parts)
    }

    @Test
    fun `Строка о консоли есть и без ссылки на консоль`() {
        val message = format(notice(StepState.FAILED, "x".repeat(501)), console = "")
        assertTrue("Полный текст — в консоли." in message.lines())
    }
}
