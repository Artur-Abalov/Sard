// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.persistence

import dev.sard.server.TestcontainersConfiguration
import org.junit.jupiter.api.BeforeAll
import org.junit.jupiter.api.TestInstance
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.context.annotation.Import
import org.springframework.jdbc.core.JdbcTemplate
import org.testcontainers.postgresql.PostgreSQLContainer
import java.sql.Connection
import java.sql.DriverManager
import java.sql.SQLException
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertTrue

private const val ROLE = "sard_self"
private const val PASSWORD = "test-only-self-dump-password"

/** insufficient_privilege: what every write of the role must end in, read-only session or not. */
private const val INSUFFICIENT_PRIVILEGE = "42501"

/**
 * F5 (D14): the role the agent next to the server dumps the database with reads every table and can
 * write nothing — not through its read-only default, which it may switch off, but through privileges.
 */
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.RANDOM_PORT,
    properties = ["spring.grpc.server.port=0"],
)
@Import(TestcontainersConfiguration::class)
@TestInstance(TestInstance.Lifecycle.PER_CLASS)
class SelfDumpRoleIntegrationTest(
    @Autowired private val jdbc: JdbcTemplate,
    @Autowired private val postgres: PostgreSQLContainer,
) {
    @BeforeAll
    fun `the role gets a password, as the server gives it one`() {
        jdbc.execute("alter role $ROLE password '$PASSWORD'")
    }

    private fun <T> asRole(block: (Connection) -> T): T {
        val connection = DriverManager.getConnection(postgres.jdbcUrl, ROLE, PASSWORD)
        return connection.use(block)
    }

    /** A session of the role that switched its read-only default off: only privileges stand in the way. */
    private fun assertDenied(sql: String) {
        val error =
            assertFailsWith<SQLException>(sql) {
                asRole { c ->
                    c.createStatement().use { s ->
                        s.execute("set default_transaction_read_only = off")
                        s.execute(sql)
                    }
                }
            }
        assertEquals(INSUFFICIENT_PRIVILEGE, error.sqlState, "$sql: ${error.message}")
    }

    @Test
    fun `the role reads every table of the database`() {
        val tables =
            jdbc.queryForList(
                "select table_name from information_schema.tables where table_schema = 'public'",
                String::class.java,
            )
        assertTrue(tables.isNotEmpty())
        asRole { c ->
            c.createStatement().use { s -> tables.forEach { s.executeQuery("select count(*) from \"$it\"").close() } }
        }
    }

    @Test
    fun `the role's sessions start read-only`() {
        val readOnly =
            asRole { c ->
                c.createStatement().use { s ->
                    s.executeQuery("show transaction_read_only").use { it.next() && it.getString(1) == "on" }
                }
            }
        assertTrue(readOnly)
    }

    @Test
    fun `the role cannot change rows`() {
        assertDenied("insert into tenants (id, name) values (gen_random_uuid(), 'x')")
        assertDenied("update tenants set name = name")
        assertDenied("delete from tenants")
        assertDenied("truncate agents")
    }

    @Test
    fun `the role cannot create objects, temporary ones included`() {
        assertDenied("create table public.self_dump_probe (id int)")
        assertDenied("create temporary table self_dump_probe (id int)")
        assertDenied("create schema self_dump_probe")
    }

    @Test
    fun `the role cannot advance sequences or store large objects`() {
        // The schema has no sequence of its own yet; one made by the owner stands for a future one.
        jdbc.execute("create sequence if not exists public.self_dump_probe_seq")
        try {
            assertDenied("select nextval('public.self_dump_probe_seq')")
            assertDenied("select setval('public.self_dump_probe_seq', 42)")
        } finally {
            jdbc.execute("drop sequence public.self_dump_probe_seq")
        }
        assertDenied("select lo_create(0)")
        assertDenied("select lo_from_bytea(0, 'x')")
    }

    @Test
    fun `the role is no more than a reader`() {
        val flags =
            jdbc.queryForMap(
                "select rolsuper, rolcreatedb, rolcreaterole, rolreplication, rolbypassrls " +
                    "from pg_roles where rolname = ?",
                ROLE,
            )
        assertTrue(flags.values.all { it == false }, flags.toString())
        val memberOf =
            jdbc.queryForList(
                "select r.rolname from pg_auth_members m join pg_roles r on r.oid = m.roleid " +
                    "join pg_roles u on u.oid = m.member where u.rolname = ?",
                String::class.java,
                ROLE,
            )
        assertEquals(listOf("pg_read_all_data"), memberOf)
    }
}
