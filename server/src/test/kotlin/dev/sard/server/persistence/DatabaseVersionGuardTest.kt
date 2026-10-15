// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.persistence

import dev.sard.server.onboarding.Database
import dev.sard.server.recovery.RecoveryCluster
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import org.flywaydb.core.Flyway
import java.nio.file.Files
import java.nio.file.Path
import java.sql.DriverManager
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith

private const val NEWER = "299901010000"
private const val NEWEST = "299901020000"

private val MIGRATION_VERSIONS: List<String> =
    checkNotNull(Thread.currentThread().contextClassLoader.getResource("db/migration")).let { dir ->
        Files.list(Path.of(dir.toURI())).use { files ->
            files
                .map { checkNotNull(Regex("V(\\d+)__.+\\.sql").matchEntire(it.fileName.toString())).groupValues[1] }
                .toList()
        }
    }

private val LAST_KNOWN = MIGRATION_VERSIONS.maxBy { it.toLong() }

/** Rule "Сервер не стартует на базе от более новой версии Sard": the guard on its own, over a migrated database. */
@MutFlowTest
class DatabaseVersionGuardTest {
    private fun migrated(): Pair<Database, Flyway> {
        val database = RecoveryCluster.restoredDatabase()
        val flyway = Flyway.configure().dataSource(database.url, database.user, database.password).load()
        return database to flyway
    }

    private fun record(
        database: Database,
        version: String,
        description: String,
        success: Boolean = true,
    ) = DriverManager.getConnection(database.url, database.user, database.password).use { c ->
        c
            .prepareStatement(
                "insert into flyway_schema_history (installed_rank, version, description, type, script, checksum, " +
                    "installed_by, installed_on, execution_time, success) " +
                    "select max(installed_rank) + 1, ?, ?, 'SQL', ?, 1, 'sard', now(), 1, ? from flyway_schema_history",
            ).use {
                it.setString(1, version)
                it.setString(2, description)
                it.setString(3, "V${version}__$description.sql")
                it.setBoolean(4, success)
                it.executeUpdate()
            }
    }

    private fun refusal(flyway: Flyway) =
        assertFailsWith<DatabaseNewerThanServer> { MutFlow.underTest { refuseNewerDatabase(flyway, "0.1.0") } }.message

    @Test
    fun `База своей версии проходит проверку`() {
        val (_, flyway) = migrated()

        MutFlow.underTest { refuseNewerDatabase(flyway, "0.1.0") }
    }

    @Test
    fun `База с неизвестной старой миграцией не считается новой`() {
        val (database, flyway) = migrated()
        record(database, "202601010000", "unknown_older")

        MutFlow.underTest { refuseNewerDatabase(flyway, "0.1.0") }
    }

    @Test
    fun `Новая применённая миграция отклоняется с обеими версиями и версией сервера`() {
        val (database, flyway) = migrated()
        record(database, NEWER, "from_the_future")

        assertEquals(
            "Database startup refused: DATABASE_NEWER_THAN_SERVER: the database has migration $NEWER " +
                "(from_the_future) applied, this server 0.1.0 knows migrations up to $LAST_KNOWN; " +
                "start the Sard version the database was backed up with, or a newer one " +
                "(docs/operator/12-server-recovery.md)",
            refusal(flyway),
        )
    }

    @Test
    fun `Неудачная новая миграция названа записанной как неудачная`() {
        val (database, flyway) = migrated()
        record(database, NEWER, "from_the_future", success = false)

        assertEquals(
            "Database startup refused: DATABASE_NEWER_THAN_SERVER: the database has migration $NEWER " +
                "(from_the_future) recorded as failed, this server 0.1.0 knows migrations up to $LAST_KNOWN; " +
                "start the Sard version the database was backed up with, or a newer one " +
                "(docs/operator/12-server-recovery.md)",
            refusal(flyway),
        )
    }

    @Test
    fun `Из нескольких новых миграций называется самая новая, в каком бы порядке они ни были записаны`() {
        val (database, flyway) = migrated()
        record(database, NEWEST, "the_newest")
        record(database, NEWER, "the_newer")

        assertEquals(
            "Database startup refused: DATABASE_NEWER_THAN_SERVER: the database has migration $NEWEST " +
                "(the_newest) applied, this server 0.1.0 knows migrations up to $LAST_KNOWN; " +
                "start the Sard version the database was backed up with, or a newer one " +
                "(docs/operator/12-server-recovery.md)",
            refusal(flyway),
        )
    }
}
