// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.database

import dev.sard.server.onboarding.TestPostgres
import org.flywaydb.core.Flyway
import org.springframework.jdbc.datasource.SingleConnectionDataSource
import java.sql.Connection
import java.sql.DriverManager

/**
 * A database of its own, prepared by db-init (the roles, the schema `sard`) and not migrated, for the tests that stop
 * the migrations at a version, put rows in and migrate on. [connection] is the superuser's, in the schema `sard`, kept
 * open so the settings of its session (`session_replication_role`) stay; the migrations run as `sard_migrator`.
 */
class MigrationSandbox {
    private val database = TestPostgres.newDatabase(migrated = false)
    private val admin = checkNotNull(database.admin)

    val connection: Connection =
        DriverManager.getConnection(database.url + "?currentSchema=sard", admin.user, admin.password)

    val dataSource = SingleConnectionDataSource(connection, true)

    /** The migrations up to and including [target], or all of them. */
    fun flyway(target: String? = null): Flyway {
        val settings = MigrationSettings(database.url, "sard_migrator", Preparation.password("sard_migrator"))
        return coreConfiguration(settings).also { if (target != null) it.target(target) }.load()
    }
}
