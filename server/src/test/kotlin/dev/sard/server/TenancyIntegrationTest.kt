// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server

import dev.sard.server.extension.TenantResolver.Companion.DEFAULT_TENANT_ID
import dev.sard.server.persistence.Agent
import dev.sard.server.persistence.AgentRepository
import jakarta.persistence.EntityManagerFactory
import org.hibernate.annotations.TenantId
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.context.annotation.Import
import org.springframework.jdbc.core.JdbcTemplate
import java.time.Instant
import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

/** Tables that describe tenants themselves or belong to tooling; everything else is tenant data. */
private val GLOBAL_TABLES = setOf("tenants", "flyway_schema_history")

/** ADR 0013: every row belongs to a tenant; the open core writes and reads the default one only. */
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.RANDOM_PORT,
    properties = ["spring.grpc.server.port=0"],
)
@Import(TestcontainersConfiguration::class)
class TenancyIntegrationTest(
    @Autowired private val jdbc: JdbcTemplate,
    @Autowired private val agents: AgentRepository,
    @Autowired private val emf: EntityManagerFactory,
) {
    @Test
    fun `the default tenant is the only tenant of a fresh install`() {
        assertEquals(listOf(DEFAULT_TENANT_ID), jdbc.queryForList("select id from tenants", UUID::class.java))
    }

    @Test
    fun `a saved agent is stamped with the default tenant`() {
        val id = UUID.randomUUID()
        agents.save(Agent(id, "db1", "1.2.3", Instant.now(), lastSeenAt = null))
        try {
            val stored = jdbc.queryForObject("select tenant_id from agents where id = ?", UUID::class.java, id)
            assertEquals(DEFAULT_TENANT_ID, stored)
            assertEquals(DEFAULT_TENANT_ID, agents.findById(id).orElseThrow().tenantId)
        } finally {
            agents.deleteById(id)
        }
    }

    @Test
    fun `rows of another tenant are invisible`() {
        val acme = UUID.randomUUID()
        val foreign = UUID.randomUUID()
        jdbc.update("insert into tenants (id, name) values (?, ?)", acme, "acme-$acme")
        jdbc.update(
            "insert into agents (id, tenant_id, hostname, agent_version, registered_at) values (?, ?, 'x', '1', now())",
            foreign,
            acme,
        )
        try {
            assertFalse(agents.findById(foreign).isPresent, "findById leaked a foreign agent")
            assertTrue(agents.findAll().none { it.id == foreign }, "findAll leaked a foreign agent")
            val ownSql = "select count(*) from agents where tenant_id = ?"
            val own = jdbc.queryForObject(ownSql, Long::class.java, DEFAULT_TENANT_ID)
            assertEquals(own, agents.count(), "count() included a foreign agent")
        } finally {
            jdbc.update("delete from agents where tenant_id = ?", acme)
            jdbc.update("delete from tenants where id = ?", acme)
        }
    }

    @Test
    fun `every tenant table has a non-null tenant_id`() {
        val tables =
            jdbc.queryForList(
                "select table_name from information_schema.tables where table_schema = 'public' order by table_name",
                String::class.java,
            )
        val tenantScoped =
            jdbc.queryForList(
                """
                select table_name from information_schema.columns
                where table_schema = 'public' and column_name = 'tenant_id' and is_nullable = 'NO'
                order by table_name
                """.trimIndent(),
                String::class.java,
            )
        assertEquals(tables - GLOBAL_TABLES, tenantScoped)
    }

    @Test
    fun `every entity carries a TenantId attribute`() {
        val untagged =
            emf.metamodel.entities
                .map { it.javaType }
                .filterNot { type -> type.declaredFields.any { it.isAnnotationPresent(TenantId::class.java) } }
        assertEquals(emptyList(), untagged.map { it.simpleName })
    }
}
