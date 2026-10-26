// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.notify

import dev.sard.server.runs.StageState
import dev.sard.server.runs.StepOutput
import dev.sard.server.verify.VerificationStatus
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import java.time.Instant
import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

private val SOURCE: UUID = UUID.fromString("0192f0c4-7a10-7000-8000-00000000b001")
private val RECORD: UUID = UUID.fromString("0192f0c4-7a10-7000-8000-00000000d001")
private const val SNAPSHOT = "4f3a9c1e7b2d5a60c8e1f2a3b4c5d6e7f8091a2b3c4d5e6f708192a3b4c5d6e7"
private const val CONSOLE = "https://sard.example.com"

private fun failed(
    mismatches: List<StepOutput.Mismatch> = emptyList(),
    stages: List<StepOutput.Stage> = listOf(stage("fingerprint", StageState.FAILED)),
    reason: String? = null,
) = VerificationNotice(
    tenantId = UUID(0, 1),
    verificationId = RECORD,
    sourceName = "db-main",
    status = VerificationStatus.FAILED,
    level = 3,
    reason = reason,
    snapshotId = SNAPSHOT,
    verifierHostname = "vrf1.example.com",
    stages = stages,
    mismatches = mismatches,
    restoreMillis = null,
    previous = VerificationStatus.PASSED,
    closedStaleEpisode = false,
)

private fun passed(
    level: Int,
    restoreMillis: Long? = null,
) = failed().copy(
    status = VerificationStatus.PASSED,
    level = level,
    stages = emptyList(),
    restoreMillis = restoreMillis,
    previous = VerificationStatus.FAILED,
)

private fun stage(
    name: String,
    state: StageState,
    output: String = "",
) = StepOutput.Stage(1, name, state, null, null, 0, output, "")

private fun orders(
    expected: String = "1204311",
    actual: String = "1204310",
) = StepOutput.Mismatch("rows:public.orders", expected, actual)

private fun stale(
    lastPassAt: Instant? = Instant.parse("2026-10-22T02:31:00Z"),
    lastOutcome: VerificationStatus? = VerificationStatus.NO_VERIFIER,
    thresholdDays: Int = 2,
) = StaleNotice(UUID(0, 1), UUID(0, 3), SOURCE, "db-main", thresholdDays, lastPassAt, lastOutcome, "Europe/Berlin")

private fun Message.text() = parts.joinToString("") { it.text }

private fun formatter(
    language: NoticeLanguage = NoticeLanguage.RU,
    console: String = CONSOLE,
) = VerificationNoticeFormatter(language, ConsoleUrl.parse(console))

/** V6 (verification-notifications.feature): the wording of the messages about restore verification. */
@MutFlowTest(includeTargets = [VerificationNoticeFormatter::class])
class VerificationNoticeFormatterTest {
    private val ru = formatter()
    private val en = formatter(NoticeLanguage.EN)

    private fun failure(
        notice: VerificationNotice,
        formatter: VerificationNoticeFormatter = ru,
    ) = checkNotNull(MutFlow.underTest { formatter.verification(notice) }).text()

    @Test
    fun `a failure in Russian names the stage, the snapshot, the verifier and the mismatch`() {
        val expected =
            """
            ❌ Проверка восстановления провалилась: db-main
            Стадия: сравнение отпечатков
            Снимок: 4f3a9c1e
            Верификатор: vrf1.example.com
            Расхождения:
            — таблица public.orders: в источнике 1 204 311 строк, в копии 1 204 310
            Проверка: 0192f0c4-7a10-7000-8000-00000000d001
            https://sard.example.com/verifications/0192f0c4-7a10-7000-8000-00000000d001
            """.trimIndent()

        assertEquals(expected, failure(failed(listOf(orders()))))
    }

    @Test
    fun `a failure in English`() {
        val expected =
            """
            ❌ Restore verification failed: db-main
            Stage: fingerprint comparison
            Snapshot: 4f3a9c1e
            Verifier: vrf1.example.com
            Mismatches:
            — table public.orders: 1,204,311 rows in the source, 1,204,310 in the copy
            Verification: 0192f0c4-7a10-7000-8000-00000000d001
            https://sard.example.com/verifications/0192f0c4-7a10-7000-8000-00000000d001
            """.trimIndent()

        assertEquals(expected, failure(failed(listOf(orders())), en))
    }

    @Test
    fun `a control path is said in words, empty or missing`() {
        val cases =
            listOf(
                Triple(ru, "empty", "— контрольный путь /srv/data пуст"),
                Triple(ru, "missing", "— контрольного пути /srv/data нет в копии"),
                Triple(en, "empty", "— control path /srv/data is empty"),
                Triple(en, "missing", "— control path /srv/data is missing in the copy"),
            )
        for ((formatter, actual, line) in cases) {
            val mismatch = StepOutput.Mismatch("control:/srv/data", "nonempty", actual)
            assertTrue(line in failure(failed(listOf(mismatch)), formatter).lines(), line)
        }
    }

    @Test
    fun `the other facts are said in words by their kind`() {
        val mismatches =
            listOf(
                StepOutput.Mismatch("tables", "42", "41"),
                StepOutput.Mismatch("files", "1000", "998"),
                StepOutput.Mismatch("bytes", "2048", "1024"),
                StepOutput.Mismatch("extension:postgis", "present", "absent"),
            )

        val lines = failure(failed(mismatches)).lines()

        assertTrue("— таблиц: в источнике 42, в копии 41" in lines, lines.toString())
        assertTrue("— файлов: в источнике 1 000, в копии 998" in lines, lines.toString())
        assertTrue("— объём: в источнике 2 КиБ, в копии 1 КиБ" in lines, lines.toString())
        assertTrue("— extension:postgis: в источнике present, в копии absent" in lines, lines.toString())
        val english = failure(failed(mismatches), en).lines()
        assertTrue("— tables: 42 in the source, 41 in the copy" in english, english.toString())
        assertTrue("— size: 2 KiB in the source, 1 KiB in the copy" in english, english.toString())
    }

    @Test
    fun `a failure without mismatches names the stage and the reason`() {
        val notice =
            failed(
                stages = listOf(stage("fingerprint_tag", StageState.FAILED)),
                reason = "fingerprint hash in the snapshot tag differs",
            )

        val lines = failure(notice).lines()

        assertEquals("Стадия: отпечаток в теге снимка", lines[1])
        assertTrue("Причина: fingerprint hash in the snapshot tag differs" in lines)
        assertFalse(lines.any { it.startsWith("Расхождения") })
    }

    @Test
    fun `an unknown stage is named as it is`() {
        val lines = failure(failed(stages = listOf(stage("future_stage", StageState.FAILED)))).lines()

        assertEquals("Стадия: future_stage", lines[1])
    }

    @Test
    fun `the first failed stage is the stage, otherwise the fingerprint comparison, otherwise none`() {
        val stages = listOf(stage("restic_check", StageState.PASSED), stage("restore", StageState.FAILED))

        assertEquals("Стадия: извлечение", failure(failed(stages = stages)).lines()[1])
        assertEquals("Стадия: сравнение отпечатков", failure(failed(listOf(orders()), stages = emptyList())).lines()[1])
        assertFalse(failure(failed(stages = emptyList(), reason = "x")).lines().any { it.startsWith("Стадия") })
    }

    @Test
    fun `only five mismatches are listed, then how many more`() {
        val many = List(8) { StepOutput.Mismatch("rows:public.t$it", "1", "2") }

        val lines = failure(failed(many)).lines()

        val start = lines.indexOf("Расхождения:")
        val shown = (0 until 5).map { "— таблица public.t$it: в источнике 1 строк, в копии 2" }
        assertEquals(shown, lines.subList(start + 1, start + 6))
        assertEquals("— и ещё 3", lines[start + 6])
        assertTrue("— and 3 more" in failure(failed(many), en).lines())
    }

    @Test
    fun `a long value is cut to 120 characters`() {
        val long = "x".repeat(500)

        val lines = failure(failed(listOf(StepOutput.Mismatch("other_fact", long, "2")))).lines()

        assertTrue("— other_fact: в источнике ${"x".repeat(120)}…, в копии 2" in lines, lines.toString())
    }

    @Test
    fun `a value of exactly 120 characters is not cut`() {
        val fits = "y".repeat(120)

        val lines = failure(failed(listOf(StepOutput.Mismatch("other_fact", fits, "2")))).lines()

        assertTrue("— other_fact: в источнике $fits, в копии 2" in lines, lines.toString())
    }

    @Test
    fun `without a console address the message has no link`() {
        val message = checkNotNull(formatter(console = "").verification(failed(listOf(orders()))))

        assertEquals("Проверка: $RECORD", message.text().lines().last())
        assertFalse("http" in message.text())
    }

    @Test
    fun `the first line is bold, the snapshot, the verifier and the record are monospaced`() {
        val message = checkNotNull(ru.verification(failed(listOf(orders()))))

        assertEquals(Message.Bold("❌ Проверка восстановления провалилась: db-main"), message.parts.first())
        val code = message.parts.filterIsInstance<Message.Code>().map { it.text }
        assertEquals(listOf("4f3a9c1e", "vrf1.example.com", "$RECORD"), code)
    }

    @Test
    fun `the output of a stage never reaches the message`() {
        val stages = listOf(stage("restore", StageState.FAILED, output = "QA-OUTPUT-MARKER"))

        assertFalse("QA-OUTPUT-MARKER" in failure(failed(stages = stages, reason = "restore failed")))
    }

    @Test
    fun `a pass after failures says the level and, at level 5, the time to restore`() {
        val expected =
            """
            ✅ Проверка восстановления снова прошла: db-main
            Уровень: 5 — время восстановления измерено
            Время восстановления: 4 мин 12 с
            Проверка: 0192f0c4-7a10-7000-8000-00000000d001
            https://sard.example.com/verifications/0192f0c4-7a10-7000-8000-00000000d001
            """.trimIndent()

        assertEquals(expected, failure(passed(5, 252_000)))
        assertEquals("Level: 4 — data matches", failure(passed(4), en).lines()[1])
        assertFalse("Время восстановления" in failure(passed(4)))
        assertFalse("Время восстановления" in failure(passed(5)), "level 5 without a measured time")
    }

    @Test
    fun `a record that is told as nothing has no message`() {
        assertEquals(null, ru.verification(failed().copy(previous = VerificationStatus.FAILED)))
        assertEquals(null, ru.verification(passed(5).copy(previous = null)))
    }

    @Test
    fun `a stale source is told in the zone of the cycle, with its last outcome`() {
        val expected =
            """
            ⚠️ Восстановление не проверено дольше 2 дн.: db-main
            Последняя успешная проверка: 2026-10-22 04:31 Europe/Berlin
            Последний итог: нет подходящего верификатора
            https://sard.example.com/sources/0192f0c4-7a10-7000-8000-00000000b001
            """.trimIndent()

        assertEquals(expected, MutFlow.underTest { ru.stale(stale()) }.text())
        val english =
            """
            ⚠️ Restore not verified for over 2 d: db-main
            Last successful verification: 2026-10-22 04:31 Europe/Berlin
            Last outcome: no fitting verifier
            https://sard.example.com/sources/0192f0c4-7a10-7000-8000-00000000b001
            """.trimIndent()
        assertEquals(english, MutFlow.underTest { en.stale(stale()) }.text())
    }

    @Test
    fun `a source never verified says so and has no last outcome`() {
        val lines = MutFlow.underTest { ru.stale(stale(lastPassAt = null, lastOutcome = null)) }.text().lines()

        assertEquals("Успешных проверок ещё не было", lines[1])
        assertFalse(lines.any { it.startsWith("Последний итог") })
        assertEquals("No successful verification yet", en.stale(stale(null, null)).text().lines()[1])
    }

    @Test
    fun `the threshold named is the one the episode opened under`() {
        assertEquals(
            "⚠️ Восстановление не проверено дольше 5 дн.: db-main",
            MutFlow
                .underTest { ru.stale(stale(thresholdDays = 5)) }
                .text()
                .lines()
                .first(),
        )
    }

    @Test
    fun `every outcome has its words in both languages`() {
        val ruWords =
            mapOf(
                VerificationStatus.FAILED to "провал",
                VerificationStatus.CANNOT_VERIFY to "не можем проверить",
                VerificationStatus.INFRASTRUCTURE_FAILURE to "сбой инфраструктуры",
                VerificationStatus.NO_VERIFIER to "нет подходящего верификатора",
                VerificationStatus.PASSED to "пройдено",
            )
        val enWords =
            mapOf(
                VerificationStatus.FAILED to "failed",
                VerificationStatus.CANNOT_VERIFY to "cannot verify",
                VerificationStatus.INFRASTRUCTURE_FAILURE to "infrastructure failure",
                VerificationStatus.NO_VERIFIER to "no fitting verifier",
                VerificationStatus.PASSED to "passed",
            )
        for (status in VerificationStatus.entries) {
            val notice = stale(lastOutcome = status)
            assertEquals("Последний итог: ${ruWords.getValue(status)}", ru.stale(notice).text().lines()[2])
            assertEquals("Last outcome: ${enWords.getValue(status)}", en.stale(notice).text().lines()[2])
        }
    }

    @Test
    fun `every level has its words in both languages`() {
        val ruWords = listOf("целостность", "извлечение", "запуск", "данные совпали", "время восстановления измерено")
        val enWords = listOf("integrity", "extraction", "application start", "data matches", "restore time measured")
        for (level in 1..5) {
            assertEquals("Уровень: $level — ${ruWords[level - 1]}", failure(passed(level)).lines()[1])
            assertEquals("Level: $level — ${enWords[level - 1]}", failure(passed(level), en).lines()[1])
        }
    }

    @Test
    fun `every stage has its words in both languages`() {
        val ruWords =
            mapOf(
                "snapshot" to "снимок",
                "fingerprint_tag" to "отпечаток в теге снимка",
                "work_volume" to "рабочий том",
                "restic_check" to "целостность хранилища",
                "sandbox" to "песочница",
                "restore" to "извлечение",
                "start" to "запуск приложения",
                "fingerprint" to "сравнение отпечатков",
                "cleanup" to "уборка",
            )
        val enWords =
            mapOf(
                "snapshot" to "snapshot",
                "fingerprint_tag" to "fingerprint tag",
                "work_volume" to "work volume",
                "restic_check" to "storage integrity",
                "sandbox" to "sandbox",
                "restore" to "extraction",
                "start" to "application start",
                "fingerprint" to "fingerprint comparison",
                "cleanup" to "cleanup",
            )
        for ((name, words) in ruWords) {
            val notice = failed(stages = listOf(stage(name, StageState.FAILED)))
            assertEquals("Стадия: $words", failure(notice).lines()[1], name)
            assertEquals("Stage: ${enWords.getValue(name)}", failure(notice, en).lines()[1], name)
        }
    }

    @Test
    fun `no verifier tells how many sources wait and where the instruction is`() {
        val expected =
            """
            ⚠️ Проверка восстановления не работает: нет верификатора
            Источников с включённой проверкой: 3
            Выпустите токен с ролью «верификатор» и поставьте агента: инструкция на обзоре консоли
            https://sard.example.com/
            """.trimIndent()
        val notice = NoVerifierNotice(UUID(0, 1), UUID(0, 4), 3)

        assertEquals(expected, MutFlow.underTest { ru.noVerifier(notice) }.text())
        val english =
            """
            ⚠️ Restore verification is not working: no verifier
            Sources to verify: 3
            Issue a token with the verifier role and install an agent: see the console overview
            https://sard.example.com/
            """.trimIndent()
        assertEquals(english, MutFlow.underTest { en.noVerifier(notice) }.text())
        assertFalse("http" in formatter(console = "").noVerifier(notice).text())
    }
}
