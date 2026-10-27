// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.database

import org.junit.jupiter.api.io.TempDir
import java.nio.file.Files
import java.nio.file.Path
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertTrue

/** Where the server and the migrate step get a password from: a file of a volume (DB2), or a variable. */
class PasswordSourceTest {
    @TempDir
    lateinit var tmp: Path

    private fun file(text: String): String = Files.writeString(tmp.resolve("password"), text).toString()

    @Test
    fun `Пароль берётся из файла, а не из переменной, если задан файл`() {
        val env = mapOf("SARD_DB_PASSWORD" to "from-variable", "SARD_DB_PASSWORD_FILE" to file("from-file"))

        assertEquals("from-file", PasswordSource.resolve(env, "SARD_DB_PASSWORD"))
    }

    @Test
    fun `Перевод строки в конце файла не входит в пароль`() {
        assertEquals(
            "secret",
            PasswordSource.resolve(mapOf("SARD_DB_PASSWORD_FILE" to file("secret\n")), "SARD_DB_PASSWORD"),
        )
        assertEquals(
            "secret",
            PasswordSource.resolve(mapOf("SARD_DB_PASSWORD_FILE" to file("secret\r\n")), "SARD_DB_PASSWORD"),
        )
    }

    @Test
    fun `Без файла пароль берётся из переменной, без неё он пуст`() {
        assertEquals(
            "from-variable",
            PasswordSource.resolve(mapOf("SARD_DB_PASSWORD" to "from-variable"), "SARD_DB_PASSWORD"),
        )
        assertEquals("", PasswordSource.resolve(emptyMap(), "SARD_DB_PASSWORD"))
        assertEquals(
            "v",
            PasswordSource.resolve(mapOf("SARD_DB_PASSWORD" to "v", "SARD_DB_PASSWORD_FILE" to ""), "SARD_DB_PASSWORD"),
        )
    }

    @Test
    fun `Файл пароля, которого нет, называет переменную и путь`() {
        val missing = tmp.resolve("absent").toString()

        val failure =
            assertFailsWith<IllegalStateException> {
                PasswordSource.resolve(
                    mapOf("SARD_DB_PASSWORD_FILE" to missing),
                    "SARD_DB_PASSWORD",
                )
            }

        assertTrue(
            "SARD_DB_PASSWORD_FILE" in failure.message.orEmpty() && missing in failure.message.orEmpty(),
            failure.message,
        )
    }
}
