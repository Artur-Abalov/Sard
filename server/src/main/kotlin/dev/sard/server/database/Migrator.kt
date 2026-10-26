// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.database

import org.flywaydb.core.Flyway
import org.flywaydb.core.api.FlywayException
import org.flywaydb.core.api.configuration.FluentConfiguration
import java.io.PrintStream
import java.sql.DriverManager
import java.time.Duration

private const val DOCS = "docs/operator/16-postgresql.md"
private const val LOCK_TIMEOUT_SECONDS = 5L
private val LOCK_TIMEOUT: Duration = Duration.ofSeconds(LOCK_TIMEOUT_SECONDS)
private const val SCHEMA = "sard"
private const val OWNER = "sard_owner"
private const val CORE_LOCATION = "classpath:db/migration"
private const val CORE_TABLE = "flyway_schema_history"
private const val EXTENSION_TABLE = "flyway_schema_history_extensions"

/** Where the migrations of enterprise extensions are looked for when nothing else is set (ADR 0013, DB2). */
const val DEFAULT_EXTENSION_LOCATION = "classpath:db/migration-extensions"

/**
 * What the migrate command needs (DB2): the database, the role that migrates and its password, where the migrations
 * of extensions are, and how long a migration may wait for a lock (D28).
 */
data class MigrationSettings(
    val url: String,
    val user: String,
    val password: String,
    val extensionLocations: List<String> = listOf(DEFAULT_EXTENSION_LOCATION),
    val lockTimeout: Duration = LOCK_TIMEOUT,
)

/** How many migrations the command applied: those of the core and those of the extensions. */
data class MigrationOutcome(
    val core: Int,
    val extensions: Int,
)

/** The migrations did not run or did not finish; the message says what to do, and the database is as it was. */
class MigrationRefused(
    message: String,
    cause: Throwable? = null,
) : IllegalStateException(message, cause)

/**
 * The migrate step (DB2, ADR 0069): Flyway as `sard_migrator`, which becomes `sard_owner` for the session (SET ROLE)
 * so that every object it creates is owned by the role without a login, and waits for a lock at most
 * [MigrationSettings.lockTimeout]. The core's migrations go first into `sard.flyway_schema_history`; the migrations
 * of extensions, if any exist, follow into a history of their own, which a core without extensions never gets.
 */
class Migrator(
    private val settings: MigrationSettings,
    private val out: PrintStream,
) {
    fun run(): MigrationOutcome {
        requirePrepared()
        val core = migrate(listOf(CORE_LOCATION), CORE_TABLE)
        val extensions = migrate(settings.extensionLocations, EXTENSION_TABLE, onlyIfAny = true)
        out.println("migrate: $core migration(s) of the core and $extensions of extensions applied")
        return MigrationOutcome(core, extensions)
    }

    private fun flyway(
        locations: List<String>,
        table: String,
    ): Flyway = configuration(settings, locations, table).load()

    private fun migrate(
        locations: List<String>,
        table: String,
        onlyIfAny: Boolean = false,
    ): Int {
        val flyway = flyway(locations, table)
        if (onlyIfAny && flyway.info().all().isEmpty()) return 0
        try {
            return flyway.migrate().migrationsExecuted
        } catch (e: FlywayException) {
            throw MigrationRefused("${describe(e)}; the failed migration changed nothing, see $DOCS", e)
        }
    }

    /** What Flyway says and, after a colon, the database's own reason (a lock timeout, a syntax error). */
    private fun describe(e: Throwable): String {
        val root = generateSequence(e) { it.cause }.last()
        return listOfNotNull(firstLine(e), reasonOf(root).takeIf { root !== e }).joinToString(": ")
    }

    private fun reasonOf(root: Throwable): String? =
        root.message
            ?.lineSequence()
            ?.firstOrNull { it.isNotBlank() }
            ?.removePrefix("ERROR: ")

    private fun firstLine(e: Throwable): String = (e.message ?: e.javaClass.simpleName).lineSequence().first()

    /**
     * db-init (or bootstrap.sql) has run: the roles are there, sard_owner has the schema, and this role may become it.
     */
    private fun requirePrepared() {
        val problem =
            try {
                DriverManager.getConnection(settings.url, settings.user, settings.password).use(::unprepared)
            } catch (e: java.sql.SQLException) {
                val why = "cannot connect to the database as ${settings.user} (SQLState ${e.sqlState})"
                throw MigrationRefused("$why; has db-init (or bootstrap.sql) run? See $DOCS", e)
            }
        if (problem != null) {
            throw MigrationRefused(
                "the database is not prepared for Sard: $problem; run db-init (docker compose up db-init) or " +
                    "bootstrap.sql first, see $DOCS",
            )
        }
    }

    private fun unprepared(connection: java.sql.Connection): String? =
        connection.createStatement().use { statement ->
            statement
                .executeQuery(
                    """
                    select exists (select from pg_roles where rolname = '$OWNER') as role,
                           (select pg_has_role(current_user, '$OWNER', 'SET') from pg_roles where rolname = '$OWNER') as can_set,
                           (select nspowner::regrole::text from pg_namespace where nspname = '$SCHEMA') as schema_owner
                    """.trimIndent(),
                ).use { row ->
                    row.next()
                    when {
                        !row.getBoolean("role") -> "the role $OWNER does not exist"
                        !row.getBoolean("can_set") -> "${settings.user} cannot become $OWNER"
                        row.getString("schema_owner") != OWNER -> "the schema $SCHEMA is missing or not owned by $OWNER"
                        else -> null
                    }
                }
        }
}

/**
 * The Flyway of the migrate step: [MigrationSettings.user] in the schema `sard`, `SET ROLE sard_owner` and the lock
 * timeout first in every session, the history in [table]. The tests of single migrations use it to stop at a version.
 */
internal fun configuration(
    settings: MigrationSettings,
    locations: List<String>,
    table: String,
): FluentConfiguration =
    Flyway
        .configure()
        .dataSource(settings.url, settings.user, settings.password)
        .locations(*locations.toTypedArray())
        .schemas(SCHEMA)
        .defaultSchema(SCHEMA)
        .createSchemas(false)
        // The schema is not empty when an extension's history starts (the core's tables are there): its own
        // history begins with a baseline before every version.
        .baselineOnMigrate(true)
        .baselineVersion("0")
        .table(table)
        .initSql("SET ROLE $OWNER; SET lock_timeout = '${settings.lockTimeout.toMillis()}ms'")

/** The Flyway of the core's migrations, for a test that migrates to a version. */
internal fun coreConfiguration(settings: MigrationSettings): FluentConfiguration =
    configuration(settings, listOf(CORE_LOCATION), CORE_TABLE)
