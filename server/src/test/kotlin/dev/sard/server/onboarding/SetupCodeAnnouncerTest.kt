// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.onboarding

import dev.sard.server.api.SetupCodeState
import dev.sard.server.selfagent.captureEvents
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

/** Rule "Сервер без администратора печатает код настройки при старте" (Р1) at the level of the component. */
@MutFlowTest
class SetupCodeAnnouncerTest {
    private val clock =
        dev.sard.server.pki
            .MovableClock(T0)
    private val codes = SetupCodes(clock) { CODE }
    private val adminSetup = FakeAdminSetup()
    private val announcer = SetupCodeAnnouncer(adminSetup, codes)

    private val codeLine = Regex("SARD SETUP CODE: ([0-9A-HJKMNP-TV-Z]{4}(-[0-9A-HJKMNP-TV-Z]{4}){6}) valid until (\\S+)")

    private fun start() = captureEvents { MutFlow.underTest { announcer.start() } }

    @Test
    fun `Старт без администратора печатает ровно одну строку с кодом и сроком`() {
        val events = start()

        val matches = events.mapNotNull { codeLine.find(it.text) }
        assertEquals(1, matches.size, events.toString())
        assertEquals(CODE, matches.single().groupValues[1])
        assertEquals("2026-10-10T12:00:00Z", matches.single().groupValues[3])
        assertEquals(ch.qos.logback.classic.Level.INFO, events.single { codeLine.containsMatchIn(it.text) }.level)
        assertEquals(SetupCodeState.ACTIVE, codes.state())
    }

    @Test
    fun `Срок печатается с точностью до секунды`() {
        clock.now = T0.plusMillis(789)

        val line = start().single { codeLine.containsMatchIn(it.text) }

        assertEquals("2026-10-10T12:00:00Z", codeLine.find(line.text)!!.groupValues[3])
    }

    @Test
    fun `Рядом со строкой кода путь мастера и команда чтения лога, и в них нет кода`() {
        val events = start()

        val index = events.indexOfFirst { codeLine.containsMatchIn(it.text) }
        val neighbours = listOfNotNull(events.getOrNull(index - 1), events.getOrNull(index + 1)).map { it.text }
        assertTrue(neighbours.any { "/setup" in it }, neighbours.toString())
        assertTrue(neighbours.any { "docker compose logs server" in it }, neighbours.toString())
        events.filterIndexed { i, _ -> i != index }.forEach {
            assertFalse(CODE in it.text || CODE.replace("-", "") in it.text, it.text)
        }
    }

    @Test
    fun `Старт с выполненным шагом admin кода не печатает и не выдаёт`() {
        adminSetup.password = "correct-horse-battery"

        val events = start()

        assertTrue(events.none { "SARD SETUP CODE" in it.text }, events.toString())
        assertEquals(SetupCodeState.NOT_ISSUED, codes.state())
    }

    @Test
    fun `Остановка не печатает ничего, а запущенный компонент сообщает, что работает`() {
        assertFalse(announcer.isRunning)
        start()
        assertTrue(announcer.isRunning)
        announcer.stop()
        assertFalse(announcer.isRunning)
    }
}
