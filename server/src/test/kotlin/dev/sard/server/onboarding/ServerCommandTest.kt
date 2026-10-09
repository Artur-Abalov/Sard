// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.onboarding

import dev.sard.server.auth.Administrators
import dev.sard.server.auth.FakeAdministrators
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import org.springframework.jdbc.BadSqlGrammarException
import java.io.ByteArrayOutputStream
import java.io.PrintStream
import java.sql.SQLException
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNull
import kotlin.test.assertTrue

private val DEFAULT_ENVIRONMENT =
    mapOf("SARD_DB_URL" to "jdbc:postgresql://db:5432/sard?user=x", "SARD_DB_USER" to "sard")

private const val HASH = "\$argon2id\$v=19\$m=19456,t=2,p=1\$c2FsdA\$aGFzaA"

/** Rule "Команда admin-reset возвращает доступ только тому, у кого есть хост сервера" (@command, Р10). */
@MutFlowTest
class ServerCommandTest {
    private val administrators = FakeAdministrators(stored = HASH)
    private val out = ByteArrayOutputStream()
    private val err = ByteArrayOutputStream()
    private var settings: DatabaseSettings? = null

    private fun run(
        vararg args: String,
        env: Map<String, String> = DEFAULT_ENVIRONMENT,
    ): Int? =
        MutFlow.underTest {
            ServerCommand { database ->
                settings = database
                administrators
            }.run(arrayOf(*args), env, PrintStream(out), PrintStream(err))
        }

    private val printed get() = out.toString() + err.toString()

    @Test
    fun `admin-reset удаляет хэш, выходит с кодом 0 и называет перезапуск`() {
        assertEquals(0, run("admin-reset"))

        assertNull(administrators.stored)
        assertTrue("removed" in printed, printed)
        assertTrue("docker compose restart server" in printed, printed)
    }

    @Test
    fun `Вывод admin-reset не содержит хэш`() {
        run("admin-reset")

        assertFalse(HASH in printed || "argon2id" in printed, printed)
    }

    @Test
    fun `admin-reset без администратора выходит с кодом 0 и говорит, что пароль не задан`() {
        administrators.stored = null

        assertEquals(0, run("admin-reset"))

        assertTrue("not set" in printed, printed)
        assertFalse("restart" in printed, printed)
    }

    @Test
    fun `admin-reset при недоступной базе выходит с кодом 1, называет базу и говорит, что ничего не изменено`() {
        administrators.down = true

        assertEquals(1, run("admin-reset"))

        assertTrue("jdbc:postgresql://db:5432/sard" in printed, printed)
        assertTrue("nothing was changed" in printed, printed)
        assertFalse("user=x" in printed, printed)
        administrators.down = false
        assertEquals(HASH, administrators.stored)
    }

    /** Administrators whose removal fails with the SQL state [state]. */
    private fun failingWith(state: String) =
        object : Administrators by administrators {
            override fun remove(): Boolean = throw BadSqlGrammarException("delete", "sql", SQLException("", state))
        }

    @Test
    fun `База без таблицы администраторов — пароль не задан`() {
        val command = ServerCommand { failingWith("42P01") }
        val code = command.run(arrayOf("admin-reset"), emptyMap(), PrintStream(out), PrintStream(err))

        assertEquals(0, code)
        assertTrue("not set" in printed, printed)
    }

    @Test
    fun `Другая ошибка базы — код 1`() {
        val command = ServerCommand { failingWith("42501") }
        val code = command.run(arrayOf("admin-reset"), emptyMap(), PrintStream(out), PrintStream(err))

        assertEquals(1, code)
        assertTrue("nothing was changed" in printed, printed)
    }

    @Test
    fun `Неизвестная команда выходит с кодом 2 и называет admin-reset, база не тронута`() {
        assertEquals(2, run("admin-rest"))

        assertTrue("admin-reset" in printed, printed)
        assertTrue("admin-rest" in printed, printed)
        assertEquals(HASH, administrators.stored)
        assertNull(settings)
    }

    @Test
    fun `Без аргументов и с аргументами Spring команды нет, сервер стартует`() {
        assertNull(run())
        assertNull(run("--server.port=0"))
        assertNull(run("--spring.profiles.active=x", "admin-reset"))
        assertEquals(HASH, administrators.stored)
        assertEquals("", printed)
    }

    @Test
    fun `Настройки базы берутся из тех же переменных, что у сервера`() {
        val env = mapOf("SARD_DB_URL" to "jdbc:postgresql://h/d", "SARD_DB_USER" to "u", "SARD_DB_PASSWORD" to "p")
        run("admin-reset", env = env)

        assertEquals(DatabaseSettings("jdbc:postgresql://h/d", "u", "p"), settings)
    }

    @Test
    fun `Без переменных база по умолчанию та же, что в application yaml`() {
        run("admin-reset", env = emptyMap())

        assertEquals(DatabaseSettings("jdbc:postgresql://localhost:5432/sard", "sard", ""), settings)
    }

    @Test
    fun `Ошибка соединения тоже код 1`() {
        administrators.down = true
        val failing = ServerCommand { administrators }

        assertEquals(1, failing.run(arrayOf("admin-reset"), emptyMap(), PrintStream(out), PrintStream(err)))
    }
}
