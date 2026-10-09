// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.runs

import dev.sard.server.TestcontainersConfiguration
import dev.sard.server.fleet.AgentPresence
import dev.sard.server.fleet.FirstSteps
import dev.sard.server.fleet.OverviewView
import dev.sard.server.fleet.Overviews
import dev.sard.server.persistence.TenantSessions
import dev.sard.server.pki.MovableClock
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.context.annotation.Import
import org.springframework.jdbc.core.JdbcTemplate
import java.sql.Timestamp
import java.util.UUID
import kotlin.test.AfterTest
import kotlin.test.BeforeTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertFalse
import kotlin.test.assertNotNull
import kotlin.test.assertNull
import kotlin.test.assertTrue

private val REPOSITORY_ID = "a0".padEnd(64, '0')

private class FixedPresence(
    var online: Set<UUID> = emptySet(),
) : AgentPresence {
    override fun online(agentId: UUID) = agentId in online

    override fun onlineIds() = online

    override fun disconnectRevoked(agentId: UUID) = Unit
}

/**
 * Mutation tests of the W2 rules that live over the database: the overview's counts and first steps, which sources
 * a plugin may back, what a run view says of its source and of a partial snapshot (W2 К14, К17, К18).
 */
@MutFlowTest
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.NONE,
    properties = ["spring.grpc.server.port=0"],
)
@Import(TestcontainersConfiguration::class, RunsTestConfiguration::class)
class W2MutationTest(
    @Autowired private val sessions: TenantSessions,
    @Autowired private val sources: Sources,
    @Autowired private val runs: Runs,
    @Autowired private val clock: MovableClock,
    @Autowired private val jdbc: JdbcTemplate,
) {
    private val tenant = RunsTenant(jdbc)
    private val other = RunsTenant(jdbc)
    private val presence = FixedPresence()
    private val overviews = Overviews(sessions, presence)

    @BeforeTest
    fun `two tenants with an agent each`() {
        clock.now = RUNS_NOW
        tenant.create()
        other.create()
    }

    @AfterTest
    fun `drop both tenants`() {
        jdbc.update("delete from enrollment_tokens where tenant_id in (?, ?)", tenant.id, other.id)
        tenant.drop()
        other.drop()
    }

    private fun overview(of: RunsTenant = tenant): OverviewView = MutFlow.underTest { overviews.of(of.id) }

    private fun steps(of: RunsTenant = tenant): List<Boolean> =
        overview(of).firstSteps.let {
            listOf(it.tokenIssued, it.agentConnected, it.repositoryInitialized, it.sourceCreated, it.backupSucceeded)
        }

    private fun token(of: RunsTenant) {
        jdbc.update(
            "insert into enrollment_tokens (id, tenant_id, label, token_hash, expires_at, created_at) " +
                "values (?, ?, ?, ?, ?, ?)",
            UUID.randomUUID(),
            of.id,
            "first",
            ByteArray(32) { it.toByte() },
            Timestamp.from(RUNS_NOW.plusSeconds(60)),
            Timestamp.from(RUNS_NOW),
        )
    }

    private val at = Timestamp.from(RUNS_NOW)

    private fun seen(agent: UUID) = jdbc.update("update agents set last_seen_at = ? where id = ?", at, agent)

    private fun revoke(agent: UUID) = jdbc.update("update agents set revoked_at = ? where id = ?", at, agent)

    private fun initialize(agent: UUID) =
        jdbc.update("update agent_repositories set repository_id = ? where agent_id = ?", REPOSITORY_ID, agent)

    private fun backup(
        partial: String?,
        status: String = "succeeded",
    ): UUID {
        val run = runs.start(tenant.id, sources.create(tenant.id, tenant.draft()).id)
        val output =
            """{"kind":"backup","snapshotId":"s1","totalBytes":10,"addedBytes":4,"repositoryId":"$REPOSITORY_ID"""" +
                (partial?.let { ""","partial":$it""" } ?: "") + "}"
        jdbc.update(
            "update run_steps set status = ?, dispatched_at = ?, finished_at = ?, output = ?::jsonb where run_id = ?",
            status,
            at,
            at,
            output,
            run.id,
        )
        jdbc.update("update runs set status = ?, finished_at = ? where id = ?", status, at, run.id)
        return run.id
    }

    @Test
    fun `the steps are complete only when every one of the five is done`() {
        val all = listOf(true, true, true, true, true)
        assertTrue(MutFlow.underTest { FirstSteps(true, true, true, true, true).complete })
        for (i in all.indices) {
            val marks = all.mapIndexed { index, mark -> mark && index != i }
            val steps = MutFlow.underTest { FirstSteps(marks[0], marks[1], marks[2], marks[3], marks[4]) }
            assertFalse(steps.complete, "step $i undone")
        }
    }

    @Test
    fun `a tenant with one fresh agent has no agent online and no step done`() {
        val view = overview()

        assertEquals(1, view.agentsTotal)
        assertEquals(0, view.agentsOnline)
        assertEquals(listOf(false, false, false, false, false), steps())
        assertEquals(0, overview(other).agentsOnline)
    }

    @Test
    fun `only a live agent that the presence names is online`() {
        val gone = UUID.randomUUID()
        tenant.insertAgent(gone)
        revoke(gone)
        presence.online = setOf(tenant.agentId, gone, other.agentId)

        val view = overview()

        assertEquals(1, view.agentsOnline)
        assertEquals(1, view.agentsTotal)
    }

    @Test
    fun `a token of any status marks the first step, of this tenant only`() {
        token(tenant)

        assertEquals(listOf(true, false, false, false, false), steps())
        assertEquals(listOf(false, false, false, false, false), steps(other))
    }

    @Test
    fun `an agent that has been seen marks the connected step unless it is revoked`() {
        seen(tenant.agentId)
        assertEquals(listOf(false, true, false, false, false), steps())

        revoke(tenant.agentId)
        assertFalse(steps()[1])
    }

    @Test
    fun `a repository with an id marks the initialized step unless its agent is revoked`() {
        initialize(tenant.agentId)
        assertEquals(listOf(false, false, true, false, false), steps())

        revoke(tenant.agentId)
        assertFalse(steps()[2])
    }

    @Test
    fun `a source marks its step until it is deleted`() {
        val source = sources.create(tenant.id, tenant.draft())
        assertEquals(listOf(false, false, false, true, false), steps())

        sources.delete(tenant.id, source.id)
        assertFalse(steps()[3])
    }

    @Test
    fun `a succeeded run marks the last step, a failed one does not`() {
        backup(null, "failed")
        assertFalse(steps()[4])

        sources.delete(tenant.id, sources.list(tenant.id, null, null, 10).single().id)
        backup(null)
        assertTrue(steps()[4])
    }

    @Test
    fun `every step done completes the overview`() {
        token(tenant)
        seen(tenant.agentId)
        initialize(tenant.agentId)
        backup(null)

        assertEquals(listOf(true, true, true, true, true), steps())
        assertTrue(overview().firstSteps.complete)
    }

    @Test
    fun `a source needs a plugin that offers backup`() {
        jdbc.update("update agent_plugins set actions = array['restore'] where agent_id = ?", tenant.agentId)

        assertEquals(
            PLUGIN,
            assertFailsWith<UnknownPlugin> { MutFlow.underTest { sources.create(tenant.id, tenant.draft()) } }.plugin,
        )
        assertEquals(0, tenant.count("sources"))
    }

    @Test
    fun `a source is not replaced onto a plugin that does not offer backup`() {
        val created = sources.create(tenant.id, tenant.draft())
        jdbc.update("update agent_plugins set actions = array['restore'] where agent_id = ?", tenant.agentId)

        assertFailsWith<UnknownPlugin> {
            MutFlow.underTest { sources.replace(tenant.id, created.id, tenant.draft()) }
        }
    }

    @Test
    fun `a system source is neither replaced nor deleted, and stays as it was`() {
        val created = sources.create(tenant.id, tenant.draft())
        jdbc.update("update sources set system_role = 'self_keys' where id = ?", created.id)

        assertFailsWith<SystemSourceProtected> {
            MutFlow.underTest { sources.replace(tenant.id, created.id, tenant.draft(name = "other")) }
        }
        assertFailsWith<SystemSourceProtected> { MutFlow.underTest { sources.delete(tenant.id, created.id) } }
        assertEquals("prod-db", sources.get(tenant.id, created.id).name)
    }

    @Test
    fun `a plugin that offers backup among other actions is accepted`() {
        val created = MutFlow.underTest { sources.create(tenant.id, tenant.draft()) }

        assertEquals(PLUGIN, created.plugin)
        assertEquals(PLUGIN, MutFlow.underTest { sources.replace(tenant.id, created.id, tenant.draft()) }.plugin)
    }

    @Test
    fun `a run names its source and says whether it was deleted`() {
        val source = sources.create(tenant.id, tenant.draft())
        val run = runs.start(tenant.id, source.id)
        val listed = { MutFlow.underTest { runs.list(tenant.id, RunFilter(), null, 10).single { it.id == run.id } } }
        assertEquals("prod-db" to false, listed().let { it.sourceName to it.sourceDeleted })
        val got = MutFlow.underTest { runs.get(tenant.id, run.id) }
        assertEquals("prod-db" to false, assertNotNull(got).let { it.sourceName to it.sourceDeleted })

        jdbc.update(
            "update run_steps set status = 'failed', dispatched_at = ?, finished_at = ? where run_id = ?",
            Timestamp.from(RUNS_NOW),
            Timestamp.from(RUNS_NOW),
            run.id,
        )
        jdbc.update("update runs set status = 'failed', finished_at = ? where id = ?", Timestamp.from(RUNS_NOW), run.id)
        sources.delete(tenant.id, source.id)

        assertEquals("prod-db" to true, listed().let { it.sourceName to it.sourceDeleted })
        assertEquals(true, MutFlow.underTest { runs.get(tenant.id, run.id) }?.sourceDeleted)
    }

    @Test
    fun `a stored output reads back whether its snapshot is partial, not partial when it says nothing`() {
        val whole = backup(null)
        assertEquals(
            false,
            MutFlow
                .underTest { runs.get(tenant.id, whole) }
                ?.steps
                ?.single()
                ?.backup
                ?.partial,
        )
        sources.delete(tenant.id, sources.list(tenant.id, null, null, 10).single().id)

        val partial = backup("true", "failed")
        assertEquals(
            true,
            MutFlow
                .underTest { runs.get(tenant.id, partial) }
                ?.steps
                ?.single()
                ?.backup
                ?.partial,
        )
        assertNull(MutFlow.underTest { runs.get(tenant.id, UUID.randomUUID()) })
    }

    private fun schema(json: String) =
        jdbc.update("update agent_plugins set config_schema = ?::jsonb where agent_id = ?", json, tenant.agentId)

    private fun refused(config: String): List<ConfigViolation> =
        assertFailsWith<InvalidConfig> {
            MutFlow.underTest { sources.create(tenant.id, tenant.draft(config = config)) }
        }.violations

    @Test
    fun `a config the plugin's schema refuses does not make a source, and the answer names the field`() {
        schema("""{"type":"object","required":["database"],"properties":{"database":{"type":"string"}}}""")

        assertEquals(listOf("config"), refused("{}").map { it.field })
        assertEquals(listOf("config/database"), refused("""{"database":5}""").map { it.field })
        assertEquals(0, tenant.count("sources"))
    }

    @Test
    fun `a secret a source names must be held by its agent`() {
        schema("""{"type":"object","properties":{"password":{"type":"string","format":"sard-secret"}}}""")
        jdbc.update("update agents set secret_names = array['db-password'] where id = ?", tenant.agentId)

        val violations = refused("""{"password":"other"}""")
        assertEquals(listOf("config/password"), violations.map { it.field })
        assertTrue("\"other\"" in violations.single().message, violations.single().message)
        val held = tenant.draft(config = """{"password":"db-password"}""")
        val ok = MutFlow.underTest { sources.create(tenant.id, held) }
        assertEquals("prod-db", ok.name)
    }

    @Test
    fun `a schema that loads a resource from outside or is no schema refuses every config`() {
        schema("""{"${'$'}ref":"classpath:schemas/string-only.json"}""")
        val outside = refused("{}").single()
        assertEquals("config", outside.field)
        assertEquals("the plugin's config schema is not a valid JSON Schema", outside.message)

        schema("""{"properties":{"a":{"pattern":"("}}}""")
        assertEquals(listOf("config"), refused("""{"a":"x"}""").map { it.field })
    }

    @Test
    fun `runs are listed by the filter they are asked with`() {
        val first = runs.start(tenant.id, sources.create(tenant.id, tenant.draft()).id)
        val second = runs.start(tenant.id, sources.create(tenant.id, tenant.draft(name = "etc")).id)
        jdbc.update("update run_steps set status = 'dispatched', dispatched_at = ? where run_id = ?", at, second.id)
        jdbc.update("update runs set status = 'dispatched' where id = ?", second.id)

        fun ids(filter: RunFilter) = MutFlow.underTest { runs.list(tenant.id, filter, null, 10) }.map { it.id }

        assertEquals(setOf(first.id, second.id), ids(RunFilter()).toSet())
        assertEquals(listOf(first.id), ids(RunFilter(sourceId = first.sourceId)))
        assertEquals(listOf(second.id), ids(RunFilter(statuses = setOf(RunState.DISPATCHED))))
    }
}
