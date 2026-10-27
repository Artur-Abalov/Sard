// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.database

import dev.sard.server.onboarding.Installation
import dev.sard.server.onboarding.RunningServer
import dev.sard.server.onboarding.StartOptions
import dev.sard.server.onboarding.TestPostgres
import org.junit.jupiter.api.io.TempDir
import org.springframework.jdbc.core.JdbcTemplate
import java.nio.file.Files
import java.nio.file.Path
import kotlin.test.AfterTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

/** The server reads the password of its database role from the file db-init wrote (DB2), as a real start. */
class DatabasePasswordFileIntegrationTest {
    @TempDir
    lateinit var tmp: Path

    private val running = mutableListOf<RunningServer>()

    @AfterTest
    fun `stop every server`() {
        running.forEach { runCatching { it.close() } }
    }

    private fun Throwable.messages() = generateSequence(this) { it.cause }.mapNotNull { it.message }.joinToString("\n")

    @Test
    fun `Сервер берёт пароль sard_app из файла SARD_DB_PASSWORD_FILE`() {
        val database = TestPostgres.newDatabase()
        val file = Files.writeString(tmp.resolve("password"), database.password + "\n")
        val options =
            StartOptions(
                properties =
                    mapOf(
                        "spring.datasource.password" to "wrong",
                        "SARD_DB_PASSWORD_FILE" to file.toString(),
                    ),
            )

        val server = Installation(tmp, database).start(options).also { running += it }

        val user = server.bean(JdbcTemplate::class.java).queryForObject("select current_user", String::class.java)
        assertEquals("sard_app", user)
    }

    @Test
    fun `Файл пароля, которого нет, не даёт серверу стартовать и называет переменную`() {
        val database = TestPostgres.newDatabase()
        val missing = tmp.resolve("absent").toString()

        val (failure, _) =
            Installation(
                tmp,
                database,
            ).startFailing(StartOptions(properties = mapOf("SARD_DB_PASSWORD_FILE" to missing)))

        assertTrue("SARD_DB_PASSWORD_FILE" in failure.messages() && missing in failure.messages(), failure.messages())
    }
}
