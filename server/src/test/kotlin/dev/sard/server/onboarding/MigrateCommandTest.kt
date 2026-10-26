// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.onboarding

import dev.sard.server.database.DEFAULT_EXTENSION_LOCATION
import dev.sard.server.database.MigrationOutcome
import dev.sard.server.database.MigrationRefused
import dev.sard.server.database.MigrationSettings
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import org.junit.jupiter.api.io.TempDir
import java.io.ByteArrayOutputStream
import java.io.PrintStream
import java.nio.file.Files
import java.nio.file.Path
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

private val ENVIRONMENT = mapOf("SARD_DB_URL" to "jdbc:postgresql://db:5432/sard", "SARD_MIGRATE_PASSWORD" to "secret")

/** The command `migrate` of the server image (DB2): the migrate service of the installation runs it. */
@MutFlowTest
class MigrateCommandTest {
    @TempDir
    lateinit var tmp: Path

    private val out = ByteArrayOutputStream()
    private val err = ByteArrayOutputStream()
    private var settings: MigrationSettings? = null
    private var failure: MigrationRefused? = null

    private fun run(
        vararg args: String,
        env: Map<String, String> = ENVIRONMENT,
    ): Int? =
        MutFlow.underTest {
            ServerCommand(migrations = { s, printer ->
                settings = s
                printer.println("migrate: done")
                failure?.let { throw it }
                MigrationOutcome(2, 0)
            }).run(arrayOf(*args), env, PrintStream(out), PrintStream(err))
        }

    private val printed get() = out.toString() + err.toString()

    @Test
    fun `migrate выходит с кодом 0 и печатает, что сделал мигратор`() {
        assertEquals(0, run("migrate"))

        assertTrue("migrate: done" in printed, printed)
    }

    @Test
    fun `migrate входит как sard_migrator, если пользователь не задан`() {
        run("migrate")

        assertEquals(
            MigrationSettings(
                "jdbc:postgresql://db:5432/sard",
                "sard_migrator",
                "secret",
                listOf(DEFAULT_EXTENSION_LOCATION),
            ),
            settings,
        )
    }

    @Test
    fun `migrate берёт пользователя, пароль из файла и расположения расширений из переменных`() {
        val file = Files.writeString(tmp.resolve("password"), "from-file\n").toString()
        val env =
            mapOf(
                "SARD_DB_URL" to "jdbc:postgresql://h/d",
                "SARD_MIGRATE_USER" to "migrator",
                "SARD_MIGRATE_PASSWORD_FILE" to file,
                "SARD_MIGRATE_EXTENSION_LOCATIONS" to "filesystem:/a, classpath:b",
            )
        run("migrate", env = env)

        val expected =
            MigrationSettings(
                "jdbc:postgresql://h/d",
                "migrator",
                "from-file",
                listOf("filesystem:/a", "classpath:b"),
            )
        assertEquals(expected, settings)
    }

    @Test
    fun `Отказ мигратора - код 1, сообщение отказа, пароля нет`() {
        failure = MigrationRefused("Migration V1__x.sql failed: boom; see docs/operator/16-postgresql.md")

        assertEquals(1, run("migrate"))

        assertTrue("Migration V1__x.sql failed: boom" in printed, printed)
        assertFalse("secret" in printed, printed)
    }

    @Test
    fun `Файл пароля, которого нет, - код 1 и имя переменной, мигратор не запускался`() {
        val env = mapOf("SARD_MIGRATE_PASSWORD_FILE" to tmp.resolve("absent").toString())

        assertEquals(1, run("migrate", env = env))

        assertTrue("SARD_MIGRATE_PASSWORD_FILE" in printed, printed)
        assertEquals(null, settings)
    }

    @Test
    fun `Неизвестная команда называет обе команды сервера`() {
        assertEquals(2, run("migrat"))

        assertTrue("admin-reset" in printed && "migrate" in printed, printed)
    }
}
