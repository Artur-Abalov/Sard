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
import kotlin.test.AfterTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertNull
import kotlin.test.assertTrue

private const val BEFORE_C1 = "202610141200"
private const val SCHEMA = "agent_certificate_renewal_migration"
private val T0 = Timestamp.from(Instant.parse("2026-10-15T12:00:00Z"))
private val T1 = Timestamp.from(Instant.parse("2026-10-16T12:00:00Z"))
private const val C0 = "0123456789abcdef0123456789abcde0"

/** The C1 migration (rule "Агент, который не продлевает сертификат, работает как раньше"), on a schema of its own. */
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.NONE,
    properties = ["spring.grpc.server.port=0", QUIET_LOOP],
)
@Import(TestcontainersConfiguration::class)
class AgentCertificateRenewalMigrationTest(
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

    private fun useSchema() = jdbc.execute("set search_path to $SCHEMA")

    private fun insertAgent(): UUID {
        val id = UUID.randomUUID()
        jdbc.update(
            "insert into agents (id, tenant_id, hostname, registered_at, role) values (?, ?, 'h', ?, 'source')",
            id,
            tenant,
            T0,
        )
        return id
    }

    private fun insertTenant() = jdbc.update("insert into tenants (id, name) values (?, ?)", tenant, "t-$tenant")

    private fun insertCertificate(
        serial: String,
        agent: UUID,
        predecessor: String? = null,
    ) = jdbc.update(
        "insert into agent_certificates (serial, tenant_id, agent_id, issued_at, not_after, predecessor_serial) " +
            "values (?, ?, ?, ?, ?, ?)",
        serial,
        tenant,
        agent,
        T0,
        Timestamp.from(T1.toInstant().plusSeconds(1)),
        predecessor,
    )

    private fun migratedWithAgents(): Pair<UUID, UUID> {
        flyway().migrate()
        useSchema()
        insertTenant()
        return insertAgent() to insertAgent()
    }

    @Test
    fun `Миграция оставляет существующие сертификаты действующими`() {
        flyway(BEFORE_C1).migrate()
        useSchema()
        insertTenant()
        val agent = insertAgent()
        jdbc.update(
            "insert into agent_certificates (serial, tenant_id, agent_id, issued_at, not_after) values (?, ?, ?, ?, ?)",
            C0,
            tenant,
            agent,
            T0,
            T1,
        )

        flyway().migrate()
        useSchema()

        val row = jdbc.queryForMap("select predecessor_serial, superseded_at, revoked_at from agent_certificates")
        assertNull(row["predecessor_serial"])
        assertNull(row["superseded_at"])
        assertNull(row["revoked_at"])
    }

    @Test
    fun `База принимает предшественника того же агента`() {
        val (agent, _) = migratedWithAgents()
        insertCertificate(C0, agent)
        insertCertificate("a".repeat(32), agent, predecessor = C0)
        val sql = "select predecessor_serial from agent_certificates where serial = ?"
        assertEquals(C0, jdbc.queryForObject(sql, String::class.java, "a".repeat(32)))
    }

    @Test
    fun `База не принимает предшественника другого агента`() {
        val (x, y) = migratedWithAgents()
        insertCertificate("b".repeat(32), y)
        val refused = assertFailsWith<DataIntegrityViolationException> { insertCertificate(C0, x, "b".repeat(32)) }
        assertTrue("agent_certificates_predecessor_fkey" in refused.message.orEmpty(), refused.message)
    }

    @Test
    fun `База не принимает несуществующего предшественника`() {
        val (x, _) = migratedWithAgents()
        val refused = assertFailsWith<DataIntegrityViolationException> { insertCertificate("c".repeat(32), x, C0) }
        assertTrue("agent_certificates_predecessor_fkey" in refused.message.orEmpty(), refused.message)
    }
}
