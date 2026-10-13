// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents

import dev.sard.server.TestcontainersConfiguration
import dev.sard.server.scheduler.QUIET_LOOP
import org.flywaydb.core.Flyway
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.context.annotation.Import
import org.springframework.dao.DataIntegrityViolationException
import org.springframework.jdbc.core.JdbcTemplate
import org.springframework.jdbc.datasource.SingleConnectionDataSource
import java.sql.Timestamp
import java.time.Instant
import java.util.UUID
import javax.sql.DataSource
import kotlin.random.Random
import kotlin.test.AfterTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertNull
import kotlin.test.assertTrue

private const val BEFORE_V1 = "202610121200"
private const val SCHEMA = "agent_role_migration"
private val T0 = Timestamp.from(Instant.parse("2026-10-12T12:00:00Z"))

/** Migration V202610121300 (V1): the role and machine id columns, on a schema of its own. */
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.NONE,
    properties = ["spring.grpc.server.port=0", QUIET_LOOP],
)
@Import(TestcontainersConfiguration::class)
class AgentRoleMigrationTest(
    @Autowired dataSource: DataSource,
) {
    private val connection = dataSource.connection
    private val single = SingleConnectionDataSource(connection, true)
    private val jdbc = JdbcTemplate(single)
    private val tenant = UUID.randomUUID()

    @AfterTest
    fun `drop the schema`() {
        jdbc.execute("set search_path to public")
        jdbc.execute("drop schema if exists $SCHEMA cascade")
        connection.close()
    }

    private fun flyway(target: String? = null) =
        Flyway
            .configure()
            .dataSource(single)
            .schemas(SCHEMA)
            .defaultSchema(SCHEMA)
            .also { if (target != null) it.target(target) }
            .load()

    /** The schema as it is after V1, rows loose of their foreign keys. */
    private fun migrated() {
        flyway().migrate()
        jdbc.execute("set search_path to $SCHEMA")
        jdbc.execute("set session_replication_role = replica")
    }

    private fun insertToken(
        builtin: Boolean,
        role: String?,
    ) = jdbc.update(
        "insert into enrollment_tokens (id, tenant_id, token_hash, expires_at, created_at, builtin, role, label) " +
            "values (?, ?, ?, ?, ?, ?, ?, '')",
        UUID.randomUUID(),
        tenant,
        Random.nextBytes(32),
        Timestamp.from(Instant.parse("2026-10-12T13:00:00Z")),
        T0,
        builtin,
        role,
    )

    private fun insertAgent(
        role: String?,
        builtin: Boolean = false,
        machineId: String? = null,
    ) = jdbc.update(
        "insert into agents (id, tenant_id, hostname, registered_at, role, builtin, machine_id) " +
            "values (?, ?, 'h', ?, ?, ?, ?)",
        UUID.randomUUID(),
        tenant,
        T0,
        role,
        builtin,
        machineId,
    )

    private fun count(table: String) = jdbc.queryForObject("select count(*) from $table", Int::class.java)

    @Test
    fun `the database refuses a built-in token with the role verifier`() {
        migrated()
        val refused =
            assertFailsWith<DataIntegrityViolationException> { insertToken(builtin = true, role = "verifier") }
        assertTrue("enrollment_tokens_builtin_source_check" in refused.message.orEmpty())
        assertEquals(0, count("enrollment_tokens"))
        insertToken(builtin = true, role = "source")
        insertToken(builtin = false, role = "verifier")
        assertEquals(2, count("enrollment_tokens"))
    }

    @Test
    fun `the database refuses a token role outside source and verifier`() {
        migrated()
        for (role in listOf(null, "admin", "Source")) {
            val refused = assertFailsWith<DataIntegrityViolationException>("$role") { insertToken(false, role) }
            assertTrue("role" in refused.message.orEmpty(), refused.message)
        }
        assertEquals(0, count("enrollment_tokens"))
    }

    @Test
    fun `the database refuses an agent role outside source and verifier`() {
        migrated()
        for (role in listOf(null, "Source", "admin", "")) {
            val refused = assertFailsWith<DataIntegrityViolationException>("$role") { insertAgent(role) }
            assertTrue("role" in refused.message.orEmpty(), refused.message)
        }
        insertAgent("verifier")
        assertEquals(1, count("agents"))
    }

    @Test
    fun `the database refuses a built-in agent with the role verifier`() {
        migrated()
        val refused = assertFailsWith<DataIntegrityViolationException> { insertAgent("verifier", builtin = true) }
        assertTrue("agents_builtin_source_check" in refused.message.orEmpty(), refused.message)
        assertEquals(0, count("agents"))
        insertAgent("source", builtin = true)
        insertAgent("verifier", builtin = false)
        assertEquals(2, count("agents"))
    }

    @Test
    fun `the database refuses a machine id that is not 64 lower-case hex digits`() {
        migrated()
        val valid = "0123456789abcdef".repeat(4)
        val malformed = listOf("", "abc", valid.uppercase(), valid + "0", valid.dropLast(1), valid.dropLast(1) + "g")
        for (machineId in malformed) {
            val refused =
                assertFailsWith<DataIntegrityViolationException>(machineId) {
                    insertAgent("source", machineId = machineId)
                }
            assertTrue("agents_machine_id_check" in refused.message.orEmpty(), refused.message)
        }
        insertAgent("source", machineId = valid)
        insertAgent("source", machineId = null)
        assertEquals(2, count("agents"))
    }

    @Test
    fun `existing agents and tokens become sources and keep no machine id`() {
        flyway(BEFORE_V1).migrate()
        jdbc.execute("set search_path to $SCHEMA")
        jdbc.execute("set session_replication_role = replica")
        val builtin = UUID.randomUUID()
        val ordinary = UUID.randomUUID()
        for ((id, isBuiltin) in listOf(builtin to true, ordinary to false)) {
            jdbc.update(
                "insert into agents (id, tenant_id, hostname, registered_at, builtin) values (?, ?, 'h', ?, ?)",
                id,
                tenant,
                T0,
                isBuiltin,
            )
        }
        for (used in listOf(true, false)) {
            jdbc.update(
                "insert into enrollment_tokens (id, tenant_id, token_hash, expires_at, created_at, used_at, label) " +
                    "values (?, ?, ?, ?, ?, ?, '')",
                UUID.randomUUID(),
                tenant,
                ByteArray(32) { (it + if (used) 1 else 2).toByte() },
                Timestamp.from(Instant.parse("2026-10-12T13:00:00Z")),
                T0,
                T0.takeIf { used },
            )
        }

        flyway().migrate()
        jdbc.execute("set search_path to $SCHEMA")

        assertEquals(listOf("source", "source"), jdbc.queryForList("select role from agents", String::class.java))
        assertEquals(
            listOf("source", "source"),
            jdbc.queryForList("select role from enrollment_tokens", String::class.java),
        )
        val withMachine = "select count(*) from agents where machine_id is not null"
        assertEquals(0, jdbc.queryForObject(withMachine, Int::class.java))
        val nullable =
            jdbc.queryForObject(
                "select is_nullable from information_schema.columns " +
                    "where table_schema = ? and table_name = 'agents' and column_name = 'machine_id'",
                String::class.java,
                SCHEMA,
            )
        assertEquals("YES", nullable)
        for (table in listOf("agents", "enrollment_tokens")) {
            val sql =
                "select is_nullable from information_schema.columns " +
                    "where table_schema = ? and table_name = ? and column_name = 'role'"
            assertEquals("NO", jdbc.queryForObject(sql, String::class.java, SCHEMA, table), table)
        }
        assertNull(
            jdbc.queryForObject(
                "select column_default from information_schema.columns " +
                    "where table_schema = ? and table_name = 'agents' and column_name = 'role'",
                String::class.java,
                SCHEMA,
            ),
        )
    }

    @Test
    fun `a schedule fire may be refused with the reason agent_role_mismatch`() {
        migrated()
        val schedule = UUID.randomUUID()
        jdbc.update(
            "insert into schedules (id, tenant_id, source_id, cron, timezone, enabled, next_run_at, created_at, " +
                "updated_at) values (?, ?, ?, '0 * * * *', 'UTC', true, ?, ?, ?)",
            schedule,
            tenant,
            UUID.randomUUID(),
            T0,
            T0,
            T0,
        )
        jdbc.update(
            "insert into schedule_fires (id, tenant_id, schedule_id, kind, scheduled_for, outcome, reason, " +
                "skipped_in_row, alert, recorded_at) values " +
                "(?, ?, ?, 'schedule', ?, 'refused', 'agent_role_mismatch', 1, false, ?)",
            UUID.randomUUID(),
            tenant,
            schedule,
            T0,
            T0,
        )
        assertEquals(1, count("schedule_fires"))
    }
}
