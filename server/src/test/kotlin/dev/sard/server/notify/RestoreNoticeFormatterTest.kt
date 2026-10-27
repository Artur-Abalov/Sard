// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.notify

import dev.sard.server.runs.RestoreResult
import dev.sard.server.runs.RunKind
import dev.sard.server.runs.RunState
import dev.sard.server.runs.StepState
import dev.sard.server.runs.Trigger
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import java.time.Instant
import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNotNull
import kotlin.test.assertNull
import kotlin.test.assertTrue

private val RUN: UUID = UUID.fromString("0192f0c4-7a10-7000-8000-00000000b001")
private val T0: Instant = Instant.parse("2026-10-14T10:00:00Z")
private val SNAPSHOT = "4f1c2a9e" + "0".repeat(56)
private const val CONSOLE = "https://sard.example.com"
private const val TARGET = "/var/lib/sard-agent/restore/etc%20data/20261010T030000Z"

private val FILES =
    RestoreResult(
        TARGET,
        filesRestored = 2,
        bytesRestored = 1536,
        null,
        null,
        null,
        ownersRestored = false,
        leftover = null,
    )

private fun restoreNotice(
    step: StepState = StepState.SUCCEEDED,
    message: String? = null,
    result: RestoreResult? = if (step == StepState.SUCCEEDED) FILES else null,
    previous: RunState? = RunState.SUCCEEDED,
    trigger: Trigger = Trigger.MANUAL,
    tookSeconds: Long = 90,
    timeoutSeconds: Long? = null,
) = RunNotice(
    tenantId = UUID.randomUUID(),
    runId = RUN,
    trigger = trigger,
    status = RunState.following(step),
    message = message,
    queuedAt = T0,
    finishedAt = T0.plusSeconds(5 + tookSeconds),
    sourceId = UUID.randomUUID(),
    sourceName = "etc data",
    agentId = UUID.randomUUID(),
    agentHostname = "db1",
    stepStatus = step,
    startedAt = T0.plusSeconds(5),
    backup = null,
    previousStatus = previous,
    kind = RunKind.RESTORE,
    restore = RestoreNotice(SNAPSHOT, Instant.parse("2026-10-10T03:00:00Z"), result),
    timeoutSeconds = timeoutSeconds,
)

private fun Message.text() = parts.joinToString("") { it.text }

/** R2a (ВК39): the notification of a restore, whatever happened before it. */
@MutFlowTest
class RestoreNoticeFormatterTest {
    private fun format(
        notice: RunNotice,
        language: NoticeLanguage = NoticeLanguage.RU,
    ): Message {
        val formatter = RunNoticeFormatter(language, ConsoleUrl.parse(CONSOLE))
        return assertNotNull(MutFlow.underTest { formatter.format(notice) })
    }

    @Test
    fun `Полный текст сообщения об успешном восстановлении files на русском`() {
        val expected =
            """
            ✅ Восстановление выполнено: etc data
            Агент: db1
            Снимок: 4f1c2a9e от 2026-10-10T03:00:00Z
            Длительность: 1 мин 30 с
            Восстановлено файлов: 2, объём: 1,5 КиБ. Куда: $TARGET
            Запуск: $RUN
            $CONSOLE/runs/$RUN
            """.trimIndent()
        assertEquals(expected, format(restoreNotice()).text())
    }

    @Test
    fun `Полный текст сообщения об успешном восстановлении files на английском`() {
        val expected =
            """
            ✅ Restore succeeded: etc data
            Agent: db1
            Snapshot: 4f1c2a9e of 2026-10-10T03:00:00Z
            Duration: 1 min 30 s
            Files restored: 2, size: 1.5 KiB. Where: $TARGET
            Run: $RUN
            $CONSOLE/runs/$RUN
            """.trimIndent()
        assertEquals(expected, format(restoreNotice(), NoticeLanguage.EN).text())
    }

    /** R2b (ВК39, РВ4): a restore of a database names the database and the tables, not the roles. */
    @Test
    fun `Успешное восстановление базы называет базу и число таблиц на обоих языках`() {
        val database = RestoreResult("app_restored_20261011", null, null, "app_restored_20261011", 12, 3, null, null)
        val notice = restoreNotice(result = database)
        val ru = format(notice).text().lines()
        val en = format(notice, NoticeLanguage.EN).text().lines()
        assertTrue("База: app_restored_20261011, таблиц: 12" in ru, ru.toString())
        assertTrue("Database: app_restored_20261011, tables: 12" in en, en.toString())
        assertTrue(ru.none { it.contains("Восстановлено файлов") || it.contains("роль") })
        assertTrue(en.none { it.contains("roles") })
    }

    @Test
    fun `Имя базы выделено кодом, остальное текстом`() {
        val database = RestoreResult("app", null, null, "app", 2, 0, null, null)
        val parts = format(restoreNotice(result = database), NoticeLanguage.EN).parts
        val at = parts.indexOf(Message.Code("app"))
        assertTrue(at > 0, parts.toString())
        assertEquals(Message.Text("Database: "), parts[at - 1])
        assertEquals(Message.Text(", tables: 2"), parts[at + 1])
    }

    // Сценарий: Уведомление о превышенном сроке называет срок и обе подсказки (восстановление)
    @Test
    fun `Уведомление о превышенном сроке восстановления называет срок, обе подсказки и что ничего не восстановлено`() {
        val notice = restoreNotice(StepState.TIMED_OUT, "deadline exceeded", timeoutSeconds = 43_200)
        val ru = format(notice).text().lines()
        val en = format(notice, NoticeLanguage.EN).text().lines()

        assertEquals("❌ Восстановление прервано по таймауту: etc data", ru.first())
        assertTrue("Срок выполнения: 12 ч" in ru, ru.toString())
        val hintRu = "Хранилище недоступно — проверьте связь с ним. Восстановление долгое — увеличьте срок у источника."
        assertTrue(hintRu in ru, ru.toString())
        assertTrue("Ничего не восстановлено: изменения отменены." in ru, ru.toString())
        assertTrue("Step timeout: 12 h" in en, en.toString())
        val hintEn =
            "If the storage is unreachable, check the connection to it. " +
                "If the restore is long, raise the source's timeout."
        assertTrue(hintEn in en, en.toString())
    }

    @Test
    fun `Заголовок зависит от статуса шага и языка`() {
        val ru =
            mapOf(
                StepState.SUCCEEDED to "✅ Восстановление выполнено",
                StepState.FAILED to "❌ Восстановление завершилось ошибкой",
                StepState.REJECTED to "❌ Восстановление отклонено агентом",
                StepState.LOST to "❌ Восстановление потеряно",
                StepState.TIMED_OUT to "❌ Восстановление прервано по таймауту",
                StepState.CANCELLED to "⏹ Восстановление отменено",
            )
        val en =
            mapOf(
                StepState.SUCCEEDED to "✅ Restore succeeded",
                StepState.FAILED to "❌ Restore failed",
                StepState.REJECTED to "❌ Restore rejected by the agent",
                StepState.LOST to "❌ Restore lost",
                StepState.TIMED_OUT to "❌ Restore timed out",
                StepState.CANCELLED to "⏹ Restore cancelled",
            )
        for ((step, headline) in ru) {
            assertEquals("$headline: etc data", format(restoreNotice(step)).text().lines().first(), step.name)
        }
        for ((step, headline) in en) {
            val first = format(restoreNotice(step), NoticeLanguage.EN).text().lines().first()
            assertEquals("$headline: etc data", first, step.name)
        }
    }

    @Test
    fun `Об ошибке сообщается причина агента и что ничего не восстановлено`() {
        val message = "TARGET_WRITE (restore): no space left on device; nothing was restored"
        val text = format(restoreNotice(StepState.FAILED, message), NoticeLanguage.EN).text()
        val lines = text.lines()
        assertTrue("Reason: $message" in lines, text)
        assertTrue("Nothing was restored: the changes were rolled back." in lines, text)
        assertTrue(lines.none { it.startsWith("Files restored") }, text)
    }

    @Test
    fun `Неудачный откат называет то, что осталось, и не говорит, что ничего не восстановлено`() {
        val left = RestoreResult(null, null, null, null, null, null, null, leftover = "database app_restored_20261011")
        val text = format(restoreNotice(StepState.FAILED, "CLEANUP_FAILED (rollback): boom", left)).text()
        assertTrue("Откат не удался, осталось: database app_restored_20261011 Удалите вручную." in text.lines(), text)
        assertTrue("Ничего не восстановлено" !in text, text)
    }

    @Test
    fun `Утерянное восстановление предлагает повторить и не утверждает, что ничего не сделано`() {
        val text = format(restoreNotice(StepState.LOST, "agent lost the step")).text()
        val repeat = "Связь с агентом прервалась, пока шаг выполнялся. Можно повторить восстановление."
        assertTrue(repeat in text.lines(), text)
        assertTrue("Ничего не восстановлено" !in text, text)
        val en = format(restoreNotice(StepState.LOST, "agent lost the step"), NoticeLanguage.EN).text()
        val again =
            "The connection to the agent was interrupted while the step was running. The restore can be started again."
        assertTrue(again in en.lines(), en)
    }

    @Test
    fun `Успешное восстановление сообщается всегда, даже после успешного запуска`() {
        for (previous in listOf(null, RunState.SUCCEEDED, RunState.FAILED)) {
            assertNotNull(
                RunNoticeFormatter(NoticeLanguage.RU, null).format(restoreNotice(previous = previous)),
                previous?.name,
            )
        }
    }

    @Test
    fun `Причина длиннее предела обрезается, как у бэкапа`() {
        val long = "x".repeat(600)
        val text = format(restoreNotice(StepState.FAILED, long)).text()
        assertTrue("Причина: ${"x".repeat(500)}…" in text.lines(), text)
        assertTrue("Полный текст — в консоли." in text.lines(), text)
    }

    @Test
    fun `Без адреса консоли ссылки нет`() {
        val text = RunNoticeFormatter(NoticeLanguage.RU, null).format(restoreNotice())!!.text()
        assertTrue(text.lines().last() == "Запуск: $RUN", text)
    }

    @Test
    fun `Восстановление, шаг которого ещё не закончен, не сообщается`() {
        val formatter = RunNoticeFormatter(NoticeLanguage.RU, null)
        for (step in listOf(StepState.QUEUED, StepState.DISPATCHED, StepState.RUNNING)) {
            assertNull(MutFlow.underTest { formatter.format(restoreNotice(step)) }, step.name)
        }
    }

    @Test
    fun `Восстановление сообщается всегда, каким бы ни был запуск, который его начал`() {
        for (trigger in Trigger.entries) {
            for (previous in listOf(null, RunState.SUCCEEDED, RunState.FAILED)) {
                // format() fails the test if the formatter says nothing
                format(restoreNotice(trigger = trigger, previous = previous), NoticeLanguage.EN)
            }
        }
    }

    @Test
    fun `Причина ровно предельной длины не обрезается, на один знак длиннее обрезается`() {
        val exact = format(restoreNotice(StepState.FAILED, "y".repeat(500))).text().lines()
        assertTrue("Причина: ${"y".repeat(500)}" in exact, exact.toString())
        assertTrue("Полный текст — в консоли." !in exact, exact.toString())

        val over = format(restoreNotice(StepState.FAILED, "y".repeat(501))).text().lines()
        assertTrue("Причина: ${"y".repeat(500)}…" in over, over.toString())
        assertTrue("Полный текст — в консоли." in over, over.toString())
    }

    @Test
    fun `Длительность считается в секундах, минутах и часах по границам единиц`() {
        val expected =
            mapOf(
                0L to "0 s",
                59L to "59 s",
                60L to "1 min 0 s",
                61L to "1 min 1 s",
                3599L to "59 min 59 s",
                3600L to "1 h 0 min",
                7500L to "2 h 5 min",
            )
        for ((seconds, text) in expected) {
            val lines = format(restoreNotice(tookSeconds = seconds), NoticeLanguage.EN).text().lines()
            assertTrue("Duration: $text" in lines, "$seconds s: $lines")
        }
    }

    @Test
    fun `Объём показан в двоичных единицах, последняя единица не заканчивается`() {
        val expected =
            mapOf(
                0L to "0 B",
                1023L to "1023 B",
                1024L to "1 KiB",
                1536L to "1.5 KiB",
                1024L * 1024 to "1 MiB",
                1L shl 40 to "1 TiB",
                1L shl 50 to "1024 TiB",
            )
        for ((bytes, text) in expected) {
            val notice = restoreNotice(result = FILES.copy(bytesRestored = bytes))
            val line = format(notice, NoticeLanguage.EN).text().lines().single { it.startsWith("Files restored") }
            assertEquals("Files restored: 2, size: $text. Where: $TARGET", line, "$bytes bytes")
        }
    }

    @Test
    fun `Пустой остаток после отката значит, что ничего не осталось`() {
        val empty = RestoreResult(null, null, null, null, null, null, null, leftover = "")
        val text = format(restoreNotice(StepState.FAILED, "boom", empty), NoticeLanguage.EN).text()
        assertTrue("Nothing was restored: the changes were rolled back." in text.lines(), text)
        assertTrue("Rollback failed" !in text, text)
    }
}
