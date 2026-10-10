// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.onboarding

import dev.sard.server.auth.Administrators
import dev.sard.server.auth.JdbcAdministrators
import org.springframework.dao.DataAccessException
import org.springframework.jdbc.core.JdbcTemplate
import org.springframework.jdbc.datasource.DriverManagerDataSource
import java.io.PrintStream
import java.sql.SQLException

private const val COMMAND = "admin-reset"
private const val OPTION_PREFIX = "--"
private const val UNDEFINED_TABLE = "42P01"
private const val CONNECT_TIMEOUT_SECONDS = "10"

/** Where the database is, from the variables the server itself reads (application.yaml). */
data class DatabaseSettings(
    val url: String,
    val user: String,
    val password: String,
) {
    companion object {
        fun of(env: Map<String, String>) =
            DatabaseSettings(
                env["SARD_DB_URL"] ?: "jdbc:postgresql://localhost:5432/sard",
                env["SARD_DB_USER"] ?: "sard",
                env["SARD_DB_PASSWORD"].orEmpty(),
            )
    }
}

/**
 * The first argument of the server image when it is a command (F4a, Р10). `admin-reset` deletes the administrator
 * hash, which opens the admin step again, and exits: no HTTP, no gRPC, no CA directory, no channel of the built-in
 * agent, because no application context is started. Exit codes: 0 done (or there was nothing to do), 1 the
 * database cannot be used and nothing was changed, 2 an unknown command. Arguments that start with "--" (Spring
 * options) and no arguments at all are no command: the server starts ([run] returns null).
 */
class ServerCommand(
    private val administrators: (DatabaseSettings) -> Administrators = ::databaseAdministrators,
) {
    fun run(
        args: Array<String>,
        env: Map<String, String>,
        out: PrintStream,
        err: PrintStream,
    ): Int? {
        val command = args.firstOrNull()?.takeUnless { it.startsWith(OPTION_PREFIX) }
        return when {
            command == null -> {
                null
            }

            command == COMMAND -> {
                reset(DatabaseSettings.of(env), out, err)
            }

            else -> {
                err.println("Unknown command '$command'. The only command of the server is $COMMAND.")
                2
            }
        }
    }

    private fun reset(
        settings: DatabaseSettings,
        out: PrintStream,
        err: PrintStream,
    ): Int =
        try {
            val removed = administrators(settings).remove()
            if (removed) {
                out.println(
                    "The administrator password is removed. Restart the server to get a new setup code: " +
                        "docker compose restart server",
                )
            } else {
                out.println("The administrator password is not set; nothing was changed.")
            }
            0
        } catch (e: DataAccessException) {
            if (isUndefinedTable(e)) {
                out.println("The administrator password is not set; nothing was changed.")
                0
            } else {
                err.println("Cannot use the database ${settings.url.substringBefore('?')}; nothing was changed.")
                1
            }
        }

    /** A database the server never started against has no table for the administrator: no password is set. */
    private fun isUndefinedTable(e: DataAccessException): Boolean =
        generateSequence<Throwable>(e) { it.cause }
            .filterIsInstance<SQLException>()
            .any { it.sqlState == UNDEFINED_TABLE }
}

private fun databaseAdministrators(settings: DatabaseSettings): Administrators {
    val dataSource = DriverManagerDataSource(settings.url, settings.user, settings.password)
    dataSource.setConnectionProperties(
        java.util.Properties().apply { setProperty("connectTimeout", CONNECT_TIMEOUT_SECONDS) },
    )
    return JdbcAdministrators(JdbcTemplate(dataSource))
}
