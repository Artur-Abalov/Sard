// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.notify

import dev.sard.server.runs.RunState
import dev.sard.server.runs.StepState
import dev.sard.server.runs.Trigger
import org.springframework.boot.test.context.ConfigDataApplicationContextInitializer
import org.springframework.boot.test.context.runner.ApplicationContextRunner
import java.time.Instant
import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNotNull
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

/**
 * S9b: the formatter is a bean of the server, set up from application.yaml and the environment
 * (SARD_NOTIFY_LANGUAGE, SARD_CONSOLE_PUBLIC_URL), and a bad value stops the start.
 */
class NoticeFormatterConfigurationTest {
    private val context =
        ApplicationContextRunner()
            .withInitializer(ConfigDataApplicationContextInitializer())
            .withUserConfiguration(NoticeFormatterConfiguration::class.java)

    private fun lines(vararg settings: String): List<String> {
        var lines: List<String> = emptyList()
        context.withPropertyValues(*settings).run {
            val formatter = it.getBean(NotificationFormatter::class.java)
            lines = assertNotNull(formatter.format(FINISHED)).parts.joinToString("") { part -> part.text }.lines()
        }
        return lines
    }

    private fun startFailure(vararg settings: String): String {
        var failure: Throwable? = null
        context.withPropertyValues(*settings).run { failure = it.startupFailure }
        return generateSequence(failure) { it.cause }.mapNotNull { it.message }.joinToString(" | ")
    }

    @Test
    fun `Язык по умолчанию — английский, ссылки без настройки нет`() {
        val text = lines()
        assertEquals("✅ Backup succeeded: db-main", text.first())
        assertEquals("Run: $RUN", text.last())
    }

    @Test
    fun `Русский язык включается настройкой окружения`() {
        assertEquals("✅ Бэкап выполнен: db-main", lines("SARD_NOTIFY_LANGUAGE=ru").first())
    }

    @Test
    fun `Ссылка строится из настройки окружения`() {
        val link = lines("SARD_CONSOLE_PUBLIC_URL=https://sard.example.com/").last()
        assertEquals("https://sard.example.com/runs/$RUN", link)
    }

    @Test
    fun `Неизвестный язык не даёт серверу стартовать`() {
        val message = startFailure("SARD_NOTIFY_LANGUAGE=de")
        assertTrue("sard.notify.language must be one of en, ru" in message, message)
    }

    @Test
    fun `Непригодный публичный адрес не даёт серверу стартовать`() {
        val unusable =
            listOf("sard.example.com", "ftp://sard.example.com", "https://", "https://x/?a=1", "https://x/#r")
        for (address in unusable) {
            val message = startFailure("SARD_CONSOLE_PUBLIC_URL=$address")
            assertTrue("sard.console.public-url" in message, "$address: $message")
        }
    }
}
