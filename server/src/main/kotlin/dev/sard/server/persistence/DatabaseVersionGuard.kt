// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.persistence

import dev.sard.server.database.EXTENSION_HISTORY_TABLE
import dev.sard.server.database.EXTENSION_LOCATIONS_SETTING
import dev.sard.server.database.extensionLocations
import org.flywaydb.core.Flyway
import org.flywaydb.core.api.MigrationInfo
import org.flywaydb.core.api.MigrationState
import org.springframework.boot.flyway.autoconfigure.FlywayMigrationStrategy
import org.springframework.boot.info.BuildProperties
import org.springframework.context.annotation.Bean
import org.springframework.context.annotation.Configuration
import org.springframework.core.env.Environment
import org.springframework.jdbc.datasource.SingleConnectionDataSource

/** A migration as the refusal names it; [failed] when the database records it as not applied. */
data class MigrationRef(
    val version: String,
    val description: String,
    val failed: Boolean = false,
)

private fun MigrationRef.outcome() = if (failed) "recorded as failed" else "applied"

/**
 * The server does not start: the database has a migration this server does not know and that is newer than all it
 * knows, applied or failed, so it belongs to a newer Sard (F7, Р31, Р32). The message starts with
 * "Database startup refused: DATABASE_NEWER_THAN_SERVER:", names both versions of migrations and the server version
 * and points to the recovery page.
 */
class DatabaseNewerThanServer(
    newest: MigrationRef,
    knownNewest: String,
    serverVersion: String,
) : IllegalStateException(
        "Database startup refused: DATABASE_NEWER_THAN_SERVER: the database has migration " +
            "${newest.version} (${newest.description}) ${newest.outcome()}, " +
            "this server $serverVersion knows migrations up to " +
            "$knownNewest; start the Sard version the database was backed up with, or a newer one " +
            "(docs/operator/12-server-recovery.md)",
    )

/**
 * Before any migration runs: a migration newer than every migration of this build stops the start, and nothing is
 * changed. Flyway ignores such rows (its default is "*:future"), failed ones too, so they are checked here. Unknown
 * older migrations are left to Flyway's own validation.
 */
fun refuseNewerDatabase(
    flyway: Flyway,
    serverVersion: String,
) {
    val all = flyway.info().all()
    val newest = newestFuture(all) ?: return
    throw DatabaseNewerThanServer(
        MigrationRef(newest.version.version, newest.description, newest.state.isFailed),
        newestKnown(all).version,
        serverVersion,
    )
}

private fun newestFuture(all: Array<MigrationInfo>) =
    all
        .filter { it.state == MigrationState.FUTURE_SUCCESS || it.state == MigrationState.FUTURE_FAILED }
        .maxByOrNull { it.version }

private fun newestKnown(all: Array<MigrationInfo>) = all.filter { it.state.isResolved }.mapNotNull { it.version }.max()

/**
 * The server does not start: a migration of this build is not applied (DB2). The server never migrates (it is
 * sard_app, which may not change the schema); the migrate step does, before the server starts. The message starts with
 * "Database startup refused: MIGRATIONS_NOT_APPLIED:" and names the first migration missing.
 */
class MigrationsNotApplied(
    first: MigrationRef,
    count: Int,
) : IllegalStateException(
        "Database startup refused: MIGRATIONS_NOT_APPLIED: $count migration(s) of this server are not applied, the " +
            "first is ${first.version} (${first.description}); run the migrate service (docker compose up migrate) " +
            "before the server (docs/operator/16-postgresql.md)",
    )

/** The migrations of this build that the database does not have applied, in order; failed ones are among them. */
private fun notApplied(flyway: Flyway) =
    flyway
        .info()
        .all()
        .filter { it.state == MigrationState.PENDING || (it.state.isFailed && it.state.isResolved) }
        .sortedBy { it.version }

/** Throws [MigrationsNotApplied] when [flyway] has a migration of this build that the database lacks. */
fun requireMigrated(flyway: Flyway) {
    val missing = notApplied(flyway)
    val first = missing.firstOrNull() ?: return
    throw MigrationsNotApplied(MigrationRef(first.version.version, first.description), missing.size)
}

/** The extensions on the classpath, with the history of their own (ADR 0069 п. 6): read like the core's, after it. */
internal fun extensionReader(
    core: Flyway,
    connection: java.sql.Connection,
    locations: List<String>,
): Flyway =
    Flyway
        .configure()
        .configuration(core.configuration)
        .dataSource(SingleConnectionDataSource(connection, true))
        .locations(*locations.toTypedArray())
        .table(EXTENSION_HISTORY_TABLE)
        .load()

/**
 * Before the server uses the database: its PostgreSQL and initdb parameters (DB1), whether its role is more than the
 * server needs (DB2), its Sard version (ADR 0060) and that the migrate step has run. Nothing is migrated here.
 */
@Configuration(proxyBeanMethods = false)
class DatabaseVersionConfiguration {
    @Bean
    fun databaseVersionGuard(
        build: BuildProperties,
        mismatches: DatabaseMismatchListener,
        excess: RoleExcessListener,
        environment: Environment,
    ) = FlywayMigrationStrategy { flyway ->
        // One connection of the pool for all of it: Flyway asks for a second one to inform its callbacks, and a pool
        // of one connection (ADR 0013, a request holds one connection at a time) would wait for it for good.
        flyway.configuration.dataSource.connection.use { connection ->
            DatabaseSettingsCheck(mismatches).check(connection)
            RoleExcessCheck(excess).check(connection)
            val reader =
                Flyway
                    .configure()
                    .configuration(flyway.configuration)
                    .dataSource(SingleConnectionDataSource(connection, true))
                    .load()
            refuseNewerDatabase(reader, build.version ?: "unknown")
            requireMigrated(reader)
            val locations = extensionLocations(environment.getProperty(EXTENSION_LOCATIONS_SETTING))
            requireExtensionsMigrated(extensionReader(reader, connection, locations))
            // What migrate would refuse to go on from: an applied migration this build lacks, a changed checksum.
            reader.validate()
        }
    }
}

/**
 * The server does not start: a migration of an extension on the classpath is not applied, or the database records it
 * as failed (CORE1 Р24). The extensions' history is a table of its own, written by the migrate step after the core's.
 * The message starts with "Database startup refused: EXTENSION_MIGRATIONS_NOT_APPLIED:" and names the first one.
 */
class ExtensionMigrationsNotApplied(
    first: MigrationRef,
    script: String,
    count: Int,
) : IllegalStateException(
        "Database startup refused: EXTENSION_MIGRATIONS_NOT_APPLIED: $count migration(s) of the extensions on the " +
            "classpath are not applied in sard.flyway_schema_history_extensions, the first is ${first.version} " +
            "(${first.description}, $script); run the migrate service (docker compose up migrate) before the " +
            "server (docs/operator/16-postgresql.md)",
    )

/**
 * Throws [ExtensionMigrationsNotApplied] when [flyway], configured with the extensions' locations and history, has a
 * migration the database lacks. No migrations on the classpath, no check, and no table of history is wanted.
 */
fun requireExtensionsMigrated(flyway: Flyway) {
    val missing = notApplied(flyway)
    val first = missing.firstOrNull() ?: return
    throw ExtensionMigrationsNotApplied(
        MigrationRef(first.version.version, first.description),
        first.script,
        missing.size,
    )
}
