// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.scheduler

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

private const val F3A = "202610091200"
private const val F3B = "202610101200"
private const val SCHEMA = "claims_migration"

/** Migration V202610101200 over data F3a wrote (Р15): who a downtime belongs to. Works in a schema of its own. */
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.NONE,
    properties = ["spring.grpc.server.port=0", QUIET_LOOP],
)
@Import(TestcontainersConfiguration::class)
class CatchUpClaimMigrationTest(
    @Autowired private val dataSource: DataSource,
) {
    private val connection = dataSource.connection
    private val single = SingleConnectionDataSource(connection, true)
    private val jdbc = JdbcTemplate(single)
    private val tenant = UUID.randomUUID()
    private val schedule = UUID.randomUUID()

    @AfterTest
    fun `drop the schema`() {
        jdbc.execute("set search_path to public")
        jdbc.execute("drop schema if exists $SCHEMA cascade")
        connection.close()
    }

    private fun flyway(target: String) =
        Flyway
            .configure()
            .dataSource(single)
            .schemas(SCHEMA)
            .defaultSchema(SCHEMA)
            .target(target)
            .load()

    private fun at(text: String): Timestamp = Timestamp.from(Instant.parse(text))

    private fun schedule(updatedAt: String) {
        jdbc.update(
            """
            insert into schedules (id, tenant_id, source_id, cron, timezone, enabled, next_run_at, created_at, updated_at)
            values (?, ?, ?, '0 * * * *', 'UTC', true, ?, ?, ?)
            """.trimIndent(),
            schedule,
            tenant,
            UUID.randomUUID(),
            at("2026-09-30T16:00:00Z"),
            at("2026-09-30T10:00:00Z"),
            at(updatedAt),
        )
    }

    private fun downtime(recorded: String): UUID = fire("schedule", "skipped_downtime", recorded, missed = 2)

    private fun catchUp(recorded: String): UUID = fire("catch_up", "refused", recorded, missed = null)

    private fun fire(
        kind: String,
        outcome: String,
        recorded: String,
        missed: Int?,
    ): UUID {
        val id = UUID.randomUUID()
        jdbc.update(
            """
            insert into schedule_fires (id, tenant_id, schedule_id, kind, scheduled_for, outcome, reason, missed_count,
                missed_until, skipped_in_row, alert, recorded_at)
            values (?, ?, ?, ?, ?, ?, ?, ?, ?, 0, false, ?)
            """.trimIndent(),
            id,
            tenant,
            schedule,
            kind,
            at(recorded),
            outcome,
            "unknown_plugin".takeIf { outcome == "refused" },
            missed,
            at(recorded).takeIf { missed != null },
            at(recorded),
        )
        return id
    }

    private fun claims(): Map<UUID, UUID?> =
        jdbc
            .queryForList("select id, catch_up_fire_id from schedule_fires where outcome = 'skipped_downtime'")
            .associate { it["id"] as UUID to it["catch_up_fire_id"] as UUID? }

    private fun migrateAround(inserting: () -> Unit) {
        flyway(F3A).migrate()
        jdbc.execute("set search_path to $SCHEMA")
        jdbc.execute("set session_replication_role = replica")
        inserting()
        flyway(F3B).migrate()
        jdbc.execute("set search_path to $SCHEMA")
    }

    @Test
    fun `a downtime of F3a data belongs to its own catch-up`() {
        val ids = mutableMapOf<String, UUID>()
        migrateAround {
            schedule("2026-09-30T10:00:00Z")
            ids["d1"] = downtime("2026-09-30T12:00:10Z")
            ids["d2"] = downtime("2026-09-30T12:00:15Z")
            ids["c1"] = catchUp("2026-09-30T12:00:20Z")
            ids["d3"] = downtime("2026-09-30T15:00:10Z")
            ids["c2"] = catchUp("2026-09-30T15:00:20Z")
        }

        val expected = mapOf(ids["d1"]!! to ids["c1"], ids["d2"]!! to ids["c1"], ids["d3"]!! to ids["c2"])
        assertEquals(expected, claims())
    }

    @Test
    fun `a downtime whose catch-up the last change cancelled belongs to nobody`() {
        lateinit var ids: List<UUID>
        migrateAround {
            schedule("2026-09-30T13:00:00Z")
            val d1 = downtime("2026-09-30T12:00:10Z")
            val d2 = downtime("2026-09-30T15:00:10Z")
            val c = catchUp("2026-09-30T15:00:20Z")
            ids = listOf(d1, d2, c)
        }

        val (d1, d2, c) = ids
        assertEquals(mapOf(d1 to null, d2 to c), claims())
    }

    @Test
    fun `a catch-up pending in F3a data is owed since the last change`() {
        migrateAround {
            schedule("2026-09-30T13:00:00Z")
            jdbc.update("update schedules set catch_up_at = ?", at("2026-09-30T14:00:00Z"))
        }

        val since = jdbc.queryForObject("select catch_up_owed_since from schedules", Timestamp::class.java)
        assertEquals(at("2026-09-30T13:00:00Z"), since)
    }
}
