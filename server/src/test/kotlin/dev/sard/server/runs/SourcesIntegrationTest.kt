// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.runs

import dev.sard.server.TestcontainersConfiguration
import dev.sard.server.pki.MovableClock
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.context.annotation.Import
import org.springframework.jdbc.core.JdbcTemplate
import java.time.Duration
import java.util.UUID
import kotlin.test.AfterTest
import kotlin.test.BeforeTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith

/** The source service S8b puts behind SourcesApi: what it stores, what it refuses (S8a contract). */
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.NONE,
    properties = ["spring.grpc.server.port=0"],
)
@Import(TestcontainersConfiguration::class, RunsTestConfiguration::class)
class SourcesIntegrationTest(
    @Autowired private val sources: Sources,
    @Autowired private val runs: Runs,
    @Autowired private val clock: MovableClock,
    @Autowired private val jdbc: JdbcTemplate,
) {
    private val tenant = RunsTenant(jdbc)
    private val other = RunsTenant(jdbc)
    private val secondAgent: UUID = UUID.randomUUID()

    @BeforeTest
    fun `two tenants with an agent each`() {
        clock.now = RUNS_NOW
        tenant.create()
        other.create()
    }

    @AfterTest
    fun `drop both tenants`() {
        tenant.drop()
        other.drop()
    }

    @Test
    fun `a created source is stored as drafted and read back`() {
        val created = sources.create(tenant.id, tenant.draft())

        val expected =
            SourceView(created.id, "prod-db", tenant.agentId, PLUGIN, REPOSITORY, created.config, RUNS_NOW, RUNS_NOW)
        assertEquals(expected, created)
        assertEquals(created, sources.get(tenant.id, created.id))
        val sql = "select config ->> 'database' from sources where id = ?"
        val stored = jdbc.queryForObject(sql, String::class.java, created.id)
        assertEquals("app", stored)
    }

    @Test
    fun `a source names an agent, plugin and repository of the agent's last Register`() {
        val unknownAgent = UUID.randomUUID()
        val agent = assertFailsWith<UnknownAgent> { sources.create(tenant.id, tenant.draft(agent = unknownAgent)) }
        assertEquals(unknownAgent, agent.agentId)
        val plugin = assertFailsWith<UnknownPlugin> { sources.create(tenant.id, tenant.draft(plugin = "mysql")) }
        assertEquals("mysql", plugin.plugin)
        assertEquals(
            "offsite",
            assertFailsWith<UnknownRepository> {
                sources.create(tenant.id, tenant.draft(repository = "offsite"))
            }.repositoryName,
        )
        assertEquals(0, tenant.count("sources"))
    }

    @Test
    fun `an agent of another tenant is unknown`() {
        assertFailsWith<UnknownAgent> { sources.create(tenant.id, tenant.draft(agent = other.agentId)) }
    }

    @Test
    fun `a revoked agent is unknown`() {
        jdbc.update("update agents set revoked_at = now() where id = ?", tenant.agentId)
        assertFailsWith<UnknownAgent> { sources.create(tenant.id, tenant.draft()) }
    }

    @Test
    fun `a live name is taken once per tenant, and free again after delete`() {
        val first = sources.create(tenant.id, tenant.draft())
        assertEquals("prod-db", assertFailsWith<SourceNameTaken> { sources.create(tenant.id, tenant.draft()) }.name)
        sources.create(other.id, other.draft())

        sources.delete(tenant.id, first.id)
        sources.create(tenant.id, tenant.draft())
        assertEquals(2, tenant.count("sources"))
    }

    @Test
    fun `replace swaps the whole source and keeps its creation time`() {
        val created = sources.create(tenant.id, tenant.draft())
        tenant.insertAgent(secondAgent, listOf("files"), listOf("offsite"))
        clock.now = RUNS_NOW + Duration.ofMinutes(5)

        val draft = tenant.draft("files-db", secondAgent, "files", "offsite", "{}")
        val replaced = sources.replace(tenant.id, created.id, draft)

        val expected = SourceView(created.id, "files-db", secondAgent, "files", "offsite", "{}", RUNS_NOW, clock.now)
        assertEquals(expected, replaced)
        assertEquals(replaced, sources.get(tenant.id, created.id))
    }

    @Test
    fun `replace checks the draft like create and changes nothing on refusal`() {
        val created = sources.create(tenant.id, tenant.draft())
        val draft = tenant.draft(repository = "offsite")
        assertFailsWith<UnknownRepository> { sources.replace(tenant.id, created.id, draft) }
        assertEquals(created, sources.get(tenant.id, created.id))
    }

    @Test
    fun `replace takes a name another live source holds`() {
        sources.create(tenant.id, tenant.draft("a"))
        val b = sources.create(tenant.id, tenant.draft("b"))
        assertFailsWith<SourceNameTaken> { sources.replace(tenant.id, b.id, tenant.draft("a")) }
    }

    @Test
    fun `a deleted source is gone for reads, replace and delete, but its row stays`() {
        val created = sources.create(tenant.id, tenant.draft())
        sources.delete(tenant.id, created.id)

        assertEquals(created.id, assertFailsWith<SourceNotFound> { sources.get(tenant.id, created.id) }.sourceId)
        assertFailsWith<SourceNotFound> { sources.replace(tenant.id, created.id, tenant.draft()) }
        assertFailsWith<SourceNotFound> { sources.delete(tenant.id, created.id) }
        assertEquals(emptyList(), sources.list(tenant.id, null, null, 50))
        assertEquals(1, tenant.count("sources"))
    }

    @Test
    fun `a source with an active run is not deleted`() {
        val created = sources.create(tenant.id, tenant.draft())
        val run = runs.start(tenant.id, created.id)

        assertEquals(run.id, assertFailsWith<RunActive> { sources.delete(tenant.id, created.id) }.activeRunId)
        tenant.finish(run.id)
        sources.delete(tenant.id, created.id)
    }

    @Test
    fun `a source of another tenant does not exist for this one`() {
        val foreign = sources.create(other.id, other.draft())
        assertFailsWith<SourceNotFound> { sources.get(tenant.id, foreign.id) }
        assertFailsWith<SourceNotFound> { sources.replace(tenant.id, foreign.id, tenant.draft()) }
        assertFailsWith<SourceNotFound> { sources.delete(tenant.id, foreign.id) }
        assertEquals(emptyList(), sources.list(tenant.id, null, null, 50))
    }

    @Test
    fun `list pages by id, filters by agent and stays in the tenant`() {
        tenant.insertAgent(secondAgent)
        val a = sources.create(tenant.id, tenant.draft("a"))
        val b = sources.create(tenant.id, tenant.draft("b", secondAgent))
        val c = sources.create(tenant.id, tenant.draft("c"))
        sources.create(other.id, other.draft())
        val ids = listOf(a, b, c).map { it.id }.sorted()

        assertEquals(ids, sources.list(tenant.id, null, null, 50).map { it.id })
        assertEquals(ids.take(2), sources.list(tenant.id, null, null, 2).map { it.id })
        assertEquals(ids.drop(1), sources.list(tenant.id, null, ids[0], 50).map { it.id })
        assertEquals(listOf(b.id), sources.list(tenant.id, secondAgent, null, 50).map { it.id })
    }
}
