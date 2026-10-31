// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.onboarding

import dev.sard.server.auth.Administrators
import dev.sard.server.auth.JdbcAdministrators
import dev.sard.server.database.EXTENSION_LOCATIONS_SETTING
import dev.sard.server.database.MigrationOutcome
import dev.sard.server.database.MigrationRefused
import dev.sard.server.database.MigrationSettings
import dev.sard.server.database.Migrator
import dev.sard.server.database.PasswordSource
import dev.sard.server.database.extensionLocations
import org.springframework.dao.DataAccessException
import org.springframework.jdbc.core.JdbcTemplate
import org.springframework.jdbc.datasource.DriverManagerDataSource
import java.io.PrintStream
import java.sql.SQLException

private const val ADMIN_RESET = "admin-reset"
private const val MIGRATE = "migrate"
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
                env["SARD_DB_USER"] ?: "sard_app",
                PasswordSource.resolve(env, "SARD_DB_PASSWORD"),
            )
    }
}

/**
 * The first argument of the server image when it is a command. `admin-reset` (F4a, Р10) deletes the administrator
 * hash, which opens the admin step again, and exits. `migrate` (DB2) applies the migrations as `sard_migrator`, the
 * step the server waits for. Neither starts HTTP, gRPC, the CA directory or the channel of the built-in agent, because
 * no application context is started. Exit codes: 0 done (or there was nothing to do), 1 the database cannot be used
 * (for `migrate`: the migrations were refused) and nothing was changed, 2 an unknown command. Arguments that start
 * with "--" (Spring options) and no arguments at all are no command: the server starts ([run] returns null).
 */
class ServerCommand(
    private val migrations: (MigrationSettings, PrintStream) -> MigrationOutcome = { settings, out ->
        Migrator(settings, out).run()
    },
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

            command == ADMIN_RESET -> {
                reset(DatabaseSettings.of(env), out, err)
            }

            command == MIGRATE -> {
                migrate(env, out, err)
            }

            else -> {
                err.println("Unknown command '$command'. The commands of the server are $ADMIN_RESET and $MIGRATE.")
                2
            }
        }
    }

    private fun migrate(
        env: Map<String, String>,
        out: PrintStream,
        err: PrintStream,
    ): Int =
        try {
            migrations(migrationSettings(env), out)
            0
        } catch (e: MigrationRefused) {
            err.println("migrate failed: ${e.message}")
            1
        } catch (e: IllegalStateException) {
            err.println("migrate failed: ${e.message}")
            1
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

private fun migrationSettings(env: Map<String, String>): MigrationSettings =
    MigrationSettings(
        env["SARD_DB_URL"] ?: DatabaseSettings.of(emptyMap()).url,
        env["SARD_MIGRATE_USER"] ?: "sard_migrator",
        PasswordSource.resolve(env, "SARD_MIGRATE_PASSWORD"),
        extensionLocations(env[EXTENSION_LOCATIONS_SETTING]),
    )

private fun databaseAdministrators(settings: DatabaseSettings): Administrators {
    val dataSource = DriverManagerDataSource(settings.url, settings.user, settings.password)
    dataSource.setConnectionProperties(
        java.util.Properties().apply {
            setProperty("connectTimeout", CONNECT_TIMEOUT_SECONDS)
            setProperty("currentSchema", "sard")
        },
    )
    return JdbcAdministrators(JdbcTemplate(dataSource))
}
