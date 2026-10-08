// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.selfagent

import ch.qos.logback.classic.Level
import org.junit.jupiter.api.io.TempDir
import java.nio.file.Files
import java.nio.file.Path
import java.security.SecureRandom
import java.sql.SQLException
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNotEquals
import kotlin.test.assertTrue

private val PASSWORD = "0123456789abcdef".repeat(4)
private val FORMAT = Regex("[0-9a-f]{64}")

/** Rule "Пароль роли sard_self генерирует сервер и хранит только в канале" (the file and the call to the role). */
class SelfAgentSetupTest {
    @TempDir
    lateinit var dir: Path

    private val applied = mutableListOf<Pair<String, String>>()
    private var failure: SQLException? = null

    private fun setup(channelDir: Path = dir) =
        SelfAgentSetup(SelfChannel(channelDir), SecureRandom()) { role, password ->
            failure?.let { throw it }
            applied += role to password
        }

    private val file get() = dir.resolve("db-password")

    @Test
    fun `Первый старт пишет пароль из 64 шестнадцатеричных символов и задаёт его роли`() {
        setup().run()

        val written = Files.readString(file)
        assertTrue(FORMAT.matches(written), written)
        assertEquals(listOf("sard_self" to written), applied)
    }

    @Test
    fun `Корректный пароль сохраняется и задаётся роли при каждом старте`() {
        Files.writeString(file, PASSWORD)

        setup().run()
        setup().run()

        assertEquals(PASSWORD, Files.readString(file))
        assertEquals(listOf("sard_self" to PASSWORD, "sard_self" to PASSWORD), applied)
    }

    @Test
    fun `Повреждённый пароль заменяется новым`() {
        for (content in listOf("", PASSWORD.drop(1), PASSWORD + "0", PASSWORD.uppercase(), PASSWORD + "\n")) {
            Files.writeString(file, content)

            setup().run()

            val written = Files.readString(file)
            assertTrue(FORMAT.matches(written), "'$content' -> '$written'")
            assertNotEquals(content, written)
            assertEquals("sard_self" to written, applied.last())
        }
    }

    @Test
    fun `Пароли разных установок различаются`() {
        val other = Files.createDirectory(dir.resolve("other"))

        setup().run()
        setup(other).run()

        assertNotEquals(Files.readString(file), Files.readString(other.resolve("db-password")))
    }

    @Test
    fun `Недоступная роль даёт одну строку WARN со ссылкой на документ, файл записан, пароля в логе нет`() {
        failure = SQLException("role \"sard_self\" does not exist", "42704")

        val lines = captureEvents { setup().run() }

        val password = Files.readString(file)
        assertTrue(FORMAT.matches(password))
        val warnings = lines.filter { it.level == Level.WARN }
        assertEquals(1, warnings.size)
        assertTrue("docs/operations/self-agent.md" in warnings.single().text, warnings.single().text)
        assertTrue("sard_self" in warnings.single().text, warnings.single().text)
        assertTrue(lines.none { password in it.text })
    }
}
