// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.e2e

import java.time.LocalDate
import java.util.Locale

private const val BYTES_PER_MIB = 1024.0 * 1024.0
private const val SECONDS_PER_MINUTE = 60.0

/**
 * What the drill measured (Р41): the dump and the database, and the minutes from the start of the first block on the
 * new VM to the server's health, to every agent online after the name is switched, and to the successful runs of the
 * self-backup. [markdown] has the row in the form of the table "Сколько это займёт" of the page; a person carries it
 * over (OQ-221).
 */
internal class DrillResult(
    private val date: LocalDate,
    private val version: String,
    private val dumpBytes: Long,
    private val databaseBytes: Long,
    private val secondsToServer: Long,
    private val secondsToAgents: Long,
    private val secondsToSelfBackup: Long,
) {
    private fun mib(bytes: Long) = String.format(Locale.ROOT, "%.1f MiB", bytes / BYTES_PER_MIB)

    private fun minutes(seconds: Long) = String.format(Locale.ROOT, "%.1f", seconds / SECONDS_PER_MINUTE)

    fun tableRow(): String =
        "| $date | $version | ${mib(dumpBytes)} | ${mib(databaseBytes)} | ${minutes(secondsToServer)} | " +
            "${minutes(secondsToAgents)} | ${minutes(secondsToSelfBackup)} |"

    fun markdown(): String =
        "| Дата | Версия | Размер дампа | Размер базы | Минут до сервера | Минут до агентов | Минут до самобэкапа |\n" +
            "|---|---|---|---|---|---|---|\n" +
            tableRow() + "\n"

    fun json(): String =
        """
        {
          "date": "$date",
          "version": "$version",
          "dumpBytes": $dumpBytes,
          "databaseBytes": $databaseBytes,
          "minutesToServer": ${minutes(secondsToServer)},
          "minutesToAgents": ${minutes(secondsToAgents)},
          "minutesToSelfBackup": ${minutes(secondsToSelfBackup)}
        }
        """.trimIndent() + "\n"
}
