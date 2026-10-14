// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.persistence

import org.flywaydb.core.Flyway
import org.flywaydb.core.api.MigrationInfo
import org.flywaydb.core.api.MigrationState
import org.springframework.boot.flyway.autoconfigure.FlywayMigrationStrategy
import org.springframework.boot.info.BuildProperties
import org.springframework.context.annotation.Bean
import org.springframework.context.annotation.Configuration

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

@Configuration(proxyBeanMethods = false)
class DatabaseVersionConfiguration {
    @Bean
    fun databaseVersionGuard(build: BuildProperties) =
        FlywayMigrationStrategy { flyway ->
            refuseNewerDatabase(flyway, build.version ?: "unknown")
            flyway.migrate()
        }
}
