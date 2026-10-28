// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.notify

import dev.sard.server.notify.telegram.TelegramHtml
import dev.sard.server.runs.IncompleteReason
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

private val RUN: UUID = UUID.fromString("0192f0c4-7a10-7000-8000-00000000a001")
private val T0: Instant = Instant.parse("2026-10-04T10:00:00Z")
private const val CONSOLE = "https://sard.example.com"
private val GIB = BackupSizes(totalBytes = 1_610_612_736, addedBytes = 12_897_485)
private val LINK = "$CONSOLE/runs/$RUN"
private const val PERMISSIONS =
    "https://github.com/Artur-Abalov/sard/blob/main/docs/plugins/files.md#от-чьего-имени-читаются-файлы"
private val TWO_PATHS = listOf("/srv/data/locked", "/srv/data/sub/x.txt")

private fun notice(
    step: StepState = StepState.FAILED,
    message: String? = "m",
    source: String = "db-main",
    host: String = "db1.example.com",
    backup: BackupSizes? = null,
    trigger: Trigger = Trigger.MANUAL,
    previous: RunState? = null,
    failuresBefore: Int = if (previous == RunState.FAILED) 1 else 0,
) = RunNotice(
    tenantId = UUID.randomUUID(),
    runId = RUN,
    trigger = trigger,
    status = RunState.following(step),
    message = message,
    queuedAt = T0,
    finishedAt = T0.plusSeconds(155),
    sourceId = UUID.randomUUID(),
    sourceName = source,
    agentId = UUID.randomUUID(),
    agentHostname = host,
    stepStatus = step,
    startedAt = T0.plusSeconds(5),
    backup = backup,
    previousStatus = previous,
    failuresBefore = failuresBefore,
)

private fun Message.text() = parts.joinToString("") { it.text }

private fun Message.lines() = text().split("\n")

/** PL7-2 (Р11 to Р13): why a snapshot of a failed backup is incomplete, and the unreadable files, in both languages. */
@MutFlowTest
class PartialNoticeFormatterTest {
    private fun format(
        notice: RunNotice,
        language: NoticeLanguage = NoticeLanguage.RU,
    ): Message {
        val formatter = RunNoticeFormatter(language, ConsoleUrl.parse(CONSOLE))
        return assertNotNull(MutFlow.underTest { formatter.format(notice) })
    }

    private fun incomplete(
        reason: IncompleteReason?,
        count: Long? = null,
        paths: List<String> = emptyList(),
    ) = GIB.copy(incompleteReason = reason, unreadableCount = count, unreadablePaths = paths)

    private fun unreadable(
        count: Long,
        paths: List<String>,
    ) = incomplete(IncompleteReason.UNREADABLE_FILES, count, paths)

    private fun failedWith(
        backup: BackupSizes,
        language: NoticeLanguage = NoticeLanguage.RU,
        message: String = "at least one source file could not be read: unreadable paths (2)",
    ) = format(notice(StepState.FAILED, message, backup = backup), language)

    @Test
    fun `Ошибка с сохранённым снимком от агента без поля сообщает о снимке нейтрально`() {
        val backup = incomplete(IncompleteReason.UNKNOWN)
        val failed = notice(StepState.FAILED, "11 files could not be read", backup = backup)
        val expected =
            """
            ❌ Бэкап завершился ошибкой: db-main
            Агент: db1.example.com
            Длительность: 2 мин 30 с
            Причина: 11 files could not be read
            Снимок создан, но бэкап не завершён, поэтому снимок считается неполным. Восстановить его можно только на хосте командой repo restore.
            Всего: 1,5 ГиБ, добавлено: 12,3 МиБ
            Запуск: $RUN
            $LINK
            """.trimIndent()
        assertEquals(expected, format(failed).text())
    }

    @Test
    fun `Строка о снимке называет причину неполноты на языке уведомления`() {
        val unreadable =
            "Снимок создан, но неполный: часть данных в него не попала. " +
                "Восстановить его можно только на хосте командой repo restore."
        val set =
            "Основной снимок сохранён полностью, но связанный с ним дополнительный снимок (отпечаток источника или " +
                "глобальные объекты PostgreSQL) не сохранился, поэтому бэкап не завершён. " +
                "Восстановить снимок можно только на хосте командой repo restore."
        val unknown =
            "Снимок создан, но бэкап не завершён, поэтому снимок считается неполным. " +
                "Восстановить его можно только на хосте командой repo restore."
        val en =
            listOf(
                "The snapshot was created but is incomplete: part of the data did not get into it. " +
                    "It can be restored only on the host, with repo restore.",
                "The main snapshot was saved in full, but the additional snapshot linked to it (the fingerprint of " +
                    "the source or the PostgreSQL global objects) was not saved, so the backup did not finish. " +
                    "The snapshot can be restored only on the host, with repo restore.",
                "The snapshot was created, but the backup did not finish, so the snapshot counts as incomplete. " +
                    "It can be restored only on the host, with repo restore.",
            )
        val backups =
            listOf(
                unreadable(1, emptyList()),
                incomplete(IncompleteReason.SNAPSHOT_SET_INCOMPLETE, 0),
                incomplete(IncompleteReason.UNKNOWN),
            )

        val ruLines = backups.map { failedWith(it).lines().let { lines -> lines[lines.size - 4] } }
        val enLines = backups.map { failedWith(it, NoticeLanguage.EN).lines().let { lines -> lines[lines.size - 4] } }

        assertEquals(listOf(unreadable, set, unknown), ruLines)
        assertEquals(en, enLines)
    }

    @Test
    fun `Ни одна строка о снимке не говорит что неполный снимок пригоден для восстановления из консоли`() {
        for (language in NoticeLanguage.entries) {
            for (reason in IncompleteReason.entries) {
                val text = failedWith(incomplete(reason, 1), language).text()

                assertTrue("repo restore" in text, "$language $reason")
                assertTrue("пригоден" !in text && "usable" !in text, "$language $reason")
            }
        }
    }

    @Test
    fun `Снимок без названной причины считается неполным по неизвестной причине`() {
        val text = failedWith(GIB).text()

        assertTrue("Снимок создан, но бэкап не завершён" in text)
    }

    @Test
    fun `Полный текст сообщения о нечитаемых файлах на русском`() {
        val expected =
            """
            ❌ Бэкап завершился ошибкой: db-main
            Агент: db1.example.com
            Длительность: 2 мин 30 с
            Причина: файлов, которые агент не смог прочитать: 2
            /srv/data/locked
            /srv/data/sub/x.txt
            Проверьте, что пользователь sard-agent может читать эти файлы: $PERMISSIONS
            Снимок создан, но неполный: часть данных в него не попала. Восстановить его можно только на хосте командой repo restore.
            Всего: 1,5 ГиБ, добавлено: 12,3 МиБ
            Запуск: $RUN
            $LINK
            """.trimIndent()
        assertEquals(expected, failedWith(unreadable(2, TWO_PATHS)).text())
    }

    @Test
    fun `Полный текст сообщения о нечитаемых файлах на английском`() {
        val expected =
            """
            ❌ Backup failed: db-main
            Agent: db1.example.com
            Duration: 2 min 30 s
            Reason: files the agent could not read: 2
            /srv/data/locked
            /srv/data/sub/x.txt
            Check that the user sard-agent can read these files: $PERMISSIONS
            The snapshot was created but is incomplete: part of the data did not get into it. It can be restored only on the host, with repo restore.
            Total: 1.5 GiB, added: 12.3 MiB
            Run: $RUN
            $LINK
            """.trimIndent()
        assertEquals(expected, failedWith(unreadable(2, TWO_PATHS), NoticeLanguage.EN).text())
    }

    @Test
    fun `Каждый путь - отдельная моноширинная часть, строка причины - части Text`() {
        val message = failedWith(unreadable(2, TWO_PATHS))

        val code = message.parts.filterIsInstance<Message.Code>().map { it.text }
        assertEquals(listOf("db1.example.com", "/srv/data/locked", "/srv/data/sub/x.txt", "$RUN"), code)
        assertTrue(message.parts.all { it is Message.Code || it.text != "/srv/data/locked" })
        assertTrue("at least one source file" !in message.text())
    }

    @Test
    fun `Остаток сверх перечисленных путей назван числом`() {
        val short = (1..10).map { "/p$it" }
        val ru = failedWith(unreadable(11, short)).lines()
        val en = failedWith(unreadable(40, short), NoticeLanguage.EN).lines()

        assertEquals(listOf("/p10", "и ещё 1"), ru.subList(13, 15))
        assertEquals(listOf("/p10", "and 30 more"), en.subList(13, 15))
        assertTrue("и ещё" !in failedWith(unreadable(2, listOf("/a", "/b"))).text())
        val none = failedWith(unreadable(3, emptyList())).lines()
        assertEquals(
            listOf(
                "Причина: файлов, которые агент не смог прочитать: 3",
                "Проверьте, что пользователь sard-agent может читать эти файлы: $PERMISSIONS",
            ),
            none.subList(3, 5),
        )
    }

    @Test
    fun `Десять путей по 50 символов перечисляются все без строки о консоли`() {
        val paths = (1..10).map { "/".padEnd(50, 'a').replaceRange(1, 3, "%02d".format(it)) }

        val text = failedWith(unreadable(10, paths)).text()

        assertTrue(paths.all { it in text })
        assertTrue("Полный текст — в консоли." !in text && "…" !in text && "и ещё" !in text)
    }

    @Test
    fun `Пути сверх бюджета 500 символов не перечисляются и названы остатком`() {
        val paths = (1..10).map { "/".padEnd(51, 'a').replaceRange(1, 3, "%02d".format(it)) }

        val lines = failedWith(unreadable(10, paths)).lines()

        assertEquals(paths.take(9), lines.subList(4, 13))
        assertEquals(listOf("и ещё 1", "Полный текст — в консоли."), lines.subList(13, 15))
    }

    @Test
    fun `Короткий путь после не поместившегося длинного не перечисляется`() {
        val paths = listOf("a".repeat(300), "b".repeat(300), "c".repeat(10))

        val lines = failedWith(unreadable(3, paths)).lines()

        assertEquals(listOf("a".repeat(300), "и ещё 2", "Полный текст — в консоли."), lines.subList(4, 7))
    }

    @Test
    fun `Первый путь длиннее 500 символов обрезается с меткой`() {
        val lines = failedWith(unreadable(1, listOf("x".repeat(600)))).lines()

        assertEquals(listOf("x".repeat(500) + "…", "Полный текст — в консоли."), lines.subList(4, 6))
        assertTrue(lines.none { it.startsWith("и ещё") })
    }

    @Test
    fun `Бюджет путей считает символы, а не байты и не половинки суррогатных пар`() {
        val path = "x".repeat(499) + "\uD83D\uDCE6\uD83D\uDCE6"

        val code = failedWith(unreadable(1, listOf(path))).parts.filterIsInstance<Message.Code>()[1]

        assertEquals("x".repeat(499) + "\uD83D\uDCE6…", code.text)
    }

    @Test
    fun `Первая ошибка серии по расписанию называет нечитаемые файлы, вторая подряд не уведомляется`() {
        val backup = unreadable(2, TWO_PATHS)
        val first = notice(trigger = Trigger.SCHEDULE, previous = RunState.SUCCEEDED, backup = backup)
        val second = first.copy(previousStatus = RunState.FAILED, failuresBefore = 1)

        assertTrue("Причина: файлов, которые агент не смог прочитать: 2" in format(first).text())
        assertTrue("/srv/data/locked" in format(first).text())
        assertNull(MutFlow.underTest { RunNoticeFormatter(NoticeLanguage.RU, null).format(second) })
    }

    @Test
    fun `Путь ровно в 500 символов перечисляется целиком`() {
        val text = failedWith(unreadable(1, listOf("x".repeat(500)))).text()

        assertTrue("x".repeat(500) in text && "…" !in text)
    }

    @Test
    fun `Подсказка о правах есть только при нечитаемых файлах`() {
        val withHint =
            mapOf(
                IncompleteReason.UNREADABLE_FILES to unreadable(1, listOf("/a")),
                IncompleteReason.SNAPSHOT_SET_INCOMPLETE to incomplete(IncompleteReason.SNAPSHOT_SET_INCOMPLETE, 0),
                IncompleteReason.UNKNOWN to incomplete(IncompleteReason.UNKNOWN),
            ).mapValues { "sard-agent" in failedWith(it.value).text() && PERMISSIONS in failedWith(it.value).text() }

        assertEquals(
            mapOf(
                IncompleteReason.UNREADABLE_FILES to true,
                IncompleteReason.SNAPSHOT_SET_INCOMPLETE to false,
                IncompleteReason.UNKNOWN to false,
            ),
            withHint,
        )
        val plain = notice(StepState.FAILED, "open /data: permission denied", backup = null)
        assertTrue("sard-agent" !in format(plain).text())
    }

    @Test
    fun `При неизвестной причине и неполном наборе снимков причина - сообщение агента`() {
        for (reason in listOf(IncompleteReason.UNKNOWN, IncompleteReason.SNAPSHOT_SET_INCOMPLETE)) {
            val text = failedWith(incomplete(reason, 0), message = "restic exited with code 1").text()

            assertTrue("Причина: restic exited with code 1" in text, "$reason")
        }
    }

    @Test
    fun `Разметка в пути остаётся текстом, маркер маскирования показан как есть`() {
        val text = failedWith(unreadable(2, listOf("/srv/<b>x</b> & y", "/srv/[REDACTED]/a.txt"))).text()

        assertTrue("/srv/<b>x</b> & y" in text && "/srv/[REDACTED]/a.txt" in text)
    }

    @Test
    fun `Сообщение с десятью путями предельной длины короче предела Telegram и кончается ссылкой на запуск`() {
        val paths = (1..10).map { "я".repeat(4096) + "…" }
        val backup = unreadable(100_000, paths)
        val long = notice(source = "s".repeat(200), host = "h".repeat(253), backup = backup)

        val html =
            dev.sard.server.notify.telegram.TelegramHtml
                .render(format(long))

        assertTrue(html.length < 4096)
        assertTrue(html.endsWith(LINK))
    }
}
