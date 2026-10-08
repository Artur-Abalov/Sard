// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.selfagent

import org.junit.jupiter.api.io.TempDir
import java.nio.file.Files
import java.nio.file.Path
import java.nio.file.attribute.PosixFilePermissions
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertFalse
import kotlin.test.assertNull
import kotlin.test.assertTrue

private val PASSWORD = "0123456789abcdef".repeat(4)

/** Rules "Заданный, но непригодный канал..." and "Пароль роли sard_self ... хранит только в канале" (files). */
class SelfChannelTest {
    @TempDir
    lateinit var dir: Path

    private fun channel() = SelfChannel(dir)

    private fun names() = Files.list(dir).use { files -> files.map { it.fileName.toString() }.sorted().toList() }

    private fun perms(path: Path) = PosixFilePermissions.toString(Files.getPosixFilePermissions(path))

    private fun message(path: Path) = assertFailsWith<IllegalStateException> { SelfChannel(path) }.message.orEmpty()

    private fun assertNamesDirAndDocs(
        message: String,
        path: Path,
    ) {
        assertTrue(path.toString() in message, message)
        assertTrue("docs/operations/self-agent.md" in message, message)
    }

    @Test
    fun `Каталог канала, которого нет, не принимается`() {
        val missing = dir.resolve("missing")
        assertNamesDirAndDocs(message(missing), missing)
    }

    @Test
    fun `Обычный файл вместо каталога канала не принимается`() {
        val file = Files.createFile(dir.resolve("plain"))
        assertNamesDirAndDocs(message(file), file)
    }

    @Test
    fun `Каталог канала, недоступный на запись, не принимается`() {
        // Not even root can create a file under /proc/self.
        val unwritable = Path.of("/proc/self")
        assertNamesDirAndDocs(message(unwritable), unwritable)
    }

    @Test
    fun `Проверка каталога не оставляет в нём файлов`() {
        channel()
        assertEquals(emptyList(), names())
    }

    @Test
    fun `Пароля в пустом канале нет`() {
        assertNull(channel().readPassword())
    }

    @Test
    fun `Записанный пароль читается, лежит без перевода строки с правами 0600`() {
        val channel = channel()
        channel.writePassword(PASSWORD)
        assertEquals(PASSWORD, channel.readPassword())
        assertEquals(PASSWORD, Files.readString(dir.resolve("db-password")))
        assertEquals("rw-------", perms(dir.resolve("db-password")))
        assertEquals(listOf("db-password"), names())
    }

    @Test
    fun `Запись пароля заменяет прежний файл целиком`() {
        val channel = channel()
        channel.writePassword("x".repeat(100))
        channel.writePassword(PASSWORD)
        assertEquals(PASSWORD, Files.readString(dir.resolve("db-password")))
        assertEquals(listOf("db-password"), names())
    }

    @Test
    fun `Повреждённый пароль читается как отсутствующий`() {
        val corrupted =
            listOf(
                "",
                PASSWORD.drop(1),
                PASSWORD + "0",
                PASSWORD.uppercase(),
                PASSWORD + "\n",
                PASSWORD.drop(1) + "g",
            )
        for (text in corrupted) {
            Files.writeString(dir.resolve("db-password"), text)
            assertNull(channel().readPassword(), "'$text'")
        }
    }

    @Test
    fun `Токена в пустом канале нет`() {
        assertNull(channel().readToken())
    }

    @Test
    fun `Записанный токен читается, лежит без перевода строки с правами 0600`() {
        val channel = channel()
        channel.writeToken("sard_token")
        assertEquals("sard_token", channel.readToken())
        assertEquals("sard_token", Files.readString(dir.resolve("enroll-token")))
        assertEquals("rw-------", perms(dir.resolve("enroll-token")))
        assertEquals(listOf("enroll-token"), names())
    }

    @Test
    fun `Удаление токена убирает файл, а без файла ничего не делает`() {
        val channel = channel()
        channel.writeToken("sard_token")
        channel.deleteToken()
        assertFalse(Files.exists(dir.resolve("enroll-token")))
        channel.deleteToken()
    }

    @Test
    fun `Путь файла токена - enroll-token в каталоге канала`() {
        assertEquals(dir.resolve("enroll-token"), channel().tokenFile)
    }
}
