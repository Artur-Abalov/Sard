// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.notify

import dev.sard.server.runs.RunState
import dev.sard.server.runs.StepState
import dev.sard.server.runs.Trigger
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import java.time.Instant
import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertNotNull
import kotlin.test.assertNull
import kotlin.test.assertTrue

private val RUN: UUID = UUID.fromString("0192f0c4-7a10-7000-8000-00000000a001")
private val NOW: Instant = Instant.parse("2026-10-04T10:00:00Z")

private val FINISHED =
    RunNotice(
        tenantId = UUID.randomUUID(),
        runId = RUN,
        trigger = Trigger.MANUAL,
        status = RunState.SUCCEEDED,
        message = null,
        queuedAt = NOW,
        finishedAt = NOW.plusSeconds(1),
        sourceId = UUID.randomUUID(),
        sourceName = "db-main",
        agentId = UUID.randomUUID(),
        agentHostname = "db1.example.com",
        stepStatus = StepState.SUCCEEDED,
        startedAt = NOW,
        backup = BackupSizes(1, 1),
    )

/** S9b: `sard.console.public-url` and `sard.notify.language`, as the formatter reads them. */
@MutFlowTest
class NoticeSettingsTest {
    private fun lastLine(address: String): String {
        val formatter = RunNoticeFormatter(NoticeLanguage.RU, MutFlow.underTest { ConsoleUrl.parse(address) })
        return assertNotNull(formatter.format(FINISHED))
            .parts
            .joinToString("") { it.text }
            .lines()
            .last()
    }

    @Test
    fun `Адрес консоли с путём и завершающими косыми чертами`() {
        val cases =
            mapOf(
                "https://sard.example.com/" to "https://sard.example.com/runs/$RUN",
                "https://sard.example.com//" to "https://sard.example.com/runs/$RUN",
                "http://10.0.0.5:8080/sard" to "http://10.0.0.5:8080/sard/runs/$RUN",
            )
        for ((address, link) in cases) assertEquals(link, lastLine(address), address)
    }

    @Test
    fun `Пустой публичный адрес — сообщение без ссылки`() {
        for (blank in listOf("", "   ")) assertNull(MutFlow.underTest { ConsoleUrl.parse(blank) })
    }

    @Test
    fun `Непригодный публичный адрес не даёт серверу стартовать`() {
        val unusable =
            listOf(
                "sard.example.com",
                "ftp://sard.example.com",
                "https://",
                "https://sard.example.com/?a=1",
                "https://sard.example.com/#runs",
                "https://sard.example.com/?",
                "https://sard.example.com/#",
                "http://:8080",
                "https://sard example.com",
            )
        for (address in unusable) {
            val failure =
                assertFailsWith<IllegalArgumentException>(address) { MutFlow.underTest { ConsoleUrl.parse(address) } }
            assertTrue("sard.console.public-url" in failure.message.orEmpty(), "$address: ${failure.message}")
        }
    }

    @Test
    fun `Язык задаётся кодом ru или en`() {
        assertEquals(NoticeLanguage.RU, MutFlow.underTest { NoticeLanguage.of("ru") })
        assertEquals(NoticeLanguage.EN, MutFlow.underTest { NoticeLanguage.of("en") })
    }

    @Test
    fun `Пустой язык — значение по умолчанию, английский`() {
        assertEquals(NoticeLanguage.EN, MutFlow.underTest { NoticeLanguage.of("") })
    }

    @Test
    fun `Неизвестный язык не даёт серверу стартовать`() {
        val failure = assertFailsWith<IllegalArgumentException> { MutFlow.underTest { NoticeLanguage.of("de") } }
        val message = failure.message.orEmpty()
        assertTrue("sard.notify.language" in message && "ru" in message && "en" in message, message)
        assertTrue("de" in message, message)
    }
}
