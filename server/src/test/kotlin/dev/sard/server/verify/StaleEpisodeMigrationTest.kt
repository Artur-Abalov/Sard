// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.verify

import dev.sard.server.TestcontainersConfiguration
import org.flywaydb.core.Flyway
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.context.annotation.Import
import org.springframework.jdbc.core.JdbcTemplate
import org.springframework.jdbc.datasource.SingleConnectionDataSource
import java.sql.Timestamp
import java.time.Instant
import java.util.UUID
import javax.sql.DataSource
import kotlin.test.AfterTest
import kotlin.test.Test
import kotlin.test.assertEquals

private const val SCHEMA = "stale_episode_migration"
private const val V6 = "202610261200"

/**
 * Migration V202610261200 and the server of the version before it (V4) still running on the migrated database, as in
 * a rollback of the binary alone: its insert of a stale episode names neither the threshold nor the zone, and must
 * still succeed, with what that server implied, seven days and UTC. Works in a schema of its own.
 */
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.NONE,
    properties = ["spring.grpc.server.port=0", "sard.scheduler.interval=1h", "sard.verification.interval=1h"],
)
@Import(TestcontainersConfiguration::class)
class StaleEpisodeMigrationTest(
    @Autowired private val dataSource: DataSource,
) {
    private val connection = dataSource.connection
    private val jdbc = JdbcTemplate(SingleConnectionDataSource(connection, true))

    @AfterTest
    fun `drop the schema`() {
        jdbc.execute("set search_path to public")
        jdbc.execute("drop schema if exists $SCHEMA cascade")
        connection.close()
    }

    @Test
    fun `a stale episode opened by the previous server version gets seven days and UTC`() {
        Flyway
            .configure()
            .dataSource(SingleConnectionDataSource(connection, true))
            .schemas(SCHEMA)
            .defaultSchema(SCHEMA)
            .target(V6)
            .load()
            .migrate()
        jdbc.execute("set search_path to $SCHEMA")
        jdbc.execute("set session_replication_role = replica")
        val now = Timestamp.from(Instant.parse("2026-10-26T12:00:00Z"))

        jdbc.update(
            "insert into verification_stale_episodes (id, tenant_id, source_id, counted_from, opened_at) " +
                "values (?, ?, ?, ?, ?)",
            UUID.randomUUID(),
            UUID.randomUUID(),
            UUID.randomUUID(),
            now,
            now,
        )

        val row = jdbc.queryForMap("select threshold_days, timezone from verification_stale_episodes")
        assertEquals(listOf<Any?>(7, "UTC"), listOf(row["threshold_days"], row["timezone"]))
    }
}
