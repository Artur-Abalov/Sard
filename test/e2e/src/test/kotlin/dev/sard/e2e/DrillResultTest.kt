// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.e2e

import java.time.LocalDate
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

/** The result of the drill (Р41, OQ-221): `result.json` and the row of the page's table "Сколько это займёт". */
class DrillResultTest {
    private val result =
        DrillResult(
            date = LocalDate.of(2026, 10, 14),
            version = "0.1.0",
            dumpBytes = 420_000,
            databaseBytes = 13_000_000,
            secondsToServer = 252,
            secondsToAgents = 306,
            secondsToSelfBackup = 534,
        )

    @Test
    fun `Строка результата в формате таблицы страницы`() {
        assertEquals("| 2026-10-14 | 0.1.0 | 0.4 MiB | 12.4 MiB | 4.2 | 5.1 | 8.9 |", result.tableRow())
    }

    @Test
    fun `result md содержит заголовок таблицы страницы и строку`() {
        val lines = result.markdown().trimEnd().lines()

        assertEquals(
            "| Дата | Версия | Размер дампа | Размер базы | Минут до сервера | Минут до агентов | Минут до самобэкапа |",
            lines[0],
        )
        assertEquals("|---|---|---|---|---|---|---|", lines[1])
        assertEquals(result.tableRow(), lines[2])
    }

    @Test
    fun `result json содержит размер дампа, размер базы и минуты до сервера, до агентов и до самобэкапа`() {
        val json = result.json()

        for (field in listOf(
            "\"date\": \"2026-10-14\"",
            "\"version\": \"0.1.0\"",
            "\"dumpBytes\": 420000",
            "\"databaseBytes\": 13000000",
            "\"minutesToServer\": 4.2",
            "\"minutesToAgents\": 5.1",
            "\"minutesToSelfBackup\": 8.9",
        )) {
            assertTrue(field in json, "$field not in $json")
        }
    }
}
