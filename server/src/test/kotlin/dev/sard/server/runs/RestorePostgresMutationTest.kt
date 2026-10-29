// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.runs

import dev.sard.server.TestcontainersConfiguration
import dev.sard.server.persistence.HibernateTenantBridge
import dev.sard.server.persistence.TenantSessions
import dev.sard.server.pki.MovableClock
import dev.sard.server.role.AgentRole
import dev.sard.server.runs.RestoreScene.Companion.PG_BACKUP
import dev.sard.server.runs.RestoreScene.Companion.PG_RESTORED
import dev.sard.server.runs.RestoreScene.Companion.PG_SCHEMA
import dev.sard.server.runs.RestoreScene.Companion.RID
import dev.sard.server.runs.RestoreScene.Companion.SF_BYTES
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import org.junit.jupiter.api.Tag
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.context.annotation.Import
import org.springframework.jdbc.core.JdbcTemplate
import tools.jackson.databind.json.JsonMapper
import java.time.Instant
import java.util.UUID
import kotlin.test.AfterTest
import kotlin.test.BeforeTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertNotNull
import kotlin.test.assertNull
import kotlin.test.assertTrue

private val JSON = JsonMapper.builder().build()
private val SQ = "5".repeat(64)
private val SQ_AT: Instant = Instant.parse("2026-10-11T03:00:00Z")

/**
 * Mutation tests of the restore of a PostgreSQL database over the database of the server (R2b, ВК32, ВК32a,
 * ВК32b): the plugin entry decides the defaults, the target and the warnings; the common code reads no key of
 * a plugin.
 */
@Tag("mutflow-isolated")
@MutFlowTest(excludeTargets = [TenantSessions::class]) // TenantSessionsTest mutates it
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.NONE,
    properties = ["spring.grpc.server.port=0"],
)
@Import(TestcontainersConfiguration::class, RunsTestConfiguration::class)
class RestorePostgresMutationTest(
    @Autowired private val restores: Restores,
    @Autowired private val sources: Sources,
    @Autowired private val runs: Runs,
    @Autowired private val queued: RecordingStepsQueued,
    @Autowired private val clock: MovableClock,
    @Autowired private val jdbc: JdbcTemplate,
) {
    private val scene = RestoreScene(jdbc, sources, runs, queued, RunsTenant(jdbc), RunsTenant(jdbc))
    private val tenant = scene.tenant
    private val a get() = scene.a

    @BeforeTest
    fun `agent A holds the repository and restores postgresql`() {
        clock.now = RUNS_NOW
        scene.create()
        scene.files(a, "postgresql")
    }

    @AfterTest
    fun `drop both tenants`() = scene.drop()

    /** The source Q of the scenarios: postgresql on agent A, the snapshot SQ of its backup. */
    private fun q(config: String = PG_BACKUP): UUID {
        val s = scene.source("Q", "postgresql", config)
        scene.snapshot(s, SQ, createdAt = SQ_AT)
        return s
    }

    private fun agent(
        host: String,
        plugins: List<String> = listOf("files", "postgresql"),
    ) = scene.agent(host, plugins = plugins)

    private fun options(source: UUID) = MutFlow.underTest { restores.options(tenant.id, source, SQ) }

    private fun draft(
        agent: Any,
        config: String = PG_RESTORED,
    ) = RestoreDraft(agent.toString(), config)

    private fun preview(
        source: UUID,
        draft: RestoreDraft,
    ) = MutFlow.underTest { restores.preview(tenant.id, source, SQ, draft) }

    private fun start(
        source: UUID,
        draft: RestoreDraft,
    ) = MutFlow.underTest { restores.start(tenant.id, source, SQ, draft) }

    private fun restoreRuns() = jdbc.queryForObject("select count(*) from runs where kind = 'restore'", Int::class.java)

    // --- the options

    @Test
    fun `the snapshot of a postgresql source is restorable`() {
        assertEquals(null, RestoreSupport.blocked("postgresql", partial = false, forgotten = false))
    }

    @Test
    fun `the form starts from the connection of the backup and a new name of the database`() {
        val s = q()

        val got = options(s)

        assertEquals("postgresql", got.snapshot.plugin)
        val target = got.targets.single()
        assertEquals(
            JSON.readTree(
                """{"host":"DB1","port":5432,"user":"backup","password_ref":"pg-app","tls_mode":"disable",""" +
                    """"database":"app_restored_20261011","restore_globals":false}""",
            ),
            JSON.readTree(target.defaults),
        )
        assertEquals(JSON.readTree(PG_SCHEMA), JSON.readTree(target.restoreConfigSchema))
    }

    @Test
    fun `a long name of the database is cut for the new name at a character, to 63 bytes`() {
        val long = "ж".repeat(60)
        val s = q("""{"host":"db1","database":"$long","user":"backup","password_ref":"pg-app"}""")

        val name = JSON.readTree(options(s).targets.single().defaults).path("database").asString()

        assertTrue(name.toByteArray().size <= 63 && name.endsWith("_restored_20261011"), name)
    }

    @Test
    fun `an agent without the restore schema of postgresql is outdated, though it restores files`() {
        val s = q()
        val b = agent("db2")
        jdbc.update(
            "update agent_plugins set restore_config_schema = null where agent_id = ? and name = 'postgresql'",
            b,
        )

        val got = options(s)

        assertEquals(listOf(a), got.targets.map { it.agentId })
        assertEquals(listOf(IneligibleAgent(b, "db2", IneligibleReason.AGENT_OUTDATED)), got.ineligible)
    }

    // --- the preview

    @Test
    fun `a preview of a database restore names the database as target, no paths, and warns about owners`() {
        val s = q()

        val got = preview(s, draft(a))

        assertEquals(
            RestorePreview(
                "Q",
                SQ,
                SQ_AT,
                SF_BYTES,
                a,
                "db9",
                "app_restored_20261011",
                emptyList(),
                false,
                listOf(RestoreWarning.PG_OWNERS_NOT_RESTORED),
            ),
            got,
        )
        assertEquals(0, restoreRuns())
    }

    @Test
    fun `choosing the global objects warns that the cluster changes, not about owners`() {
        val s = q()
        val config =
            """{"host":"other-db","user":"restorer","password_ref":"pg-app","database":"x","restore_globals":true}"""

        val got = preview(s, draft(a, config))

        assertEquals(true, got.restoreGlobals)
        assertEquals("x", got.target)
        assertEquals(listOf(RestoreWarning.GLOBALS_CHANGE_CLUSTER), got.warnings)
    }

    @Test
    fun `the same cluster is told by the agent and the address of the backup`() {
        val s = q()
        val b = agent("db2")

        fun warns(
            agent: UUID,
            host: String,
            port: String = "",
        ): Boolean {
            val config = """{"host":"$host"$port,"user":"r","password_ref":"pg-app","database":"x"}"""
            return RestoreWarning.PG_SAME_CLUSTER in preview(s, draft(agent, config)).warnings
        }

        assertTrue(warns(a, "db1", ""","port":5432"""))
        assertTrue(warns(a, "DB1"), "the port is 5432 by default, the host has no case")
        assertTrue(!warns(a, "db1", ""","port":5433"""))
        assertTrue(!warns(a, "db9"))
        assertTrue(!warns(b, "db1", ""","port":5432"""), "another agent")
    }

    @Test
    fun `the warning about the same cluster does not stop the restore`() {
        val s = q()
        val config = """{"host":"db1","user":"r","password_ref":"pg-app","database":"x"}"""

        assertTrue(RestoreWarning.PG_SAME_CLUSTER in preview(s, draft(a, config)).warnings)
        val run = start(s, draft(a, config))

        assertEquals(RunState.QUEUED, run.status)
        assertEquals(1, restoreRuns())
    }

    // --- the run

    @Test
    fun `the restore run keeps the database as its target and sends the config to the agent`() {
        val s = q()

        val run = start(s, draft(a))

        assertEquals(RunKind.RESTORE, run.kind)
        assertEquals("app_restored_20261011", run.restore?.target)
        assertEquals(false, run.partial, "a restore run backs nothing up, so it is not partial")
        val step = run.steps.single()
        assertEquals("postgresql", step.plugin)
        assertEquals(Action.RESTORE, step.action)
        assertEquals(PG_RESTORED, step.config)
        assertEquals(SQ, step.snapshotId)
        assertEquals("main", step.repositoryName)
        val held = "select repository_id from snapshots where snapshot_id = ?"
        assertEquals(RID, jdbc.queryForObject(held, String::class.java, SQ))
        assertEquals(listOf(tenant.id to a), queued.calls.toList())
        assertEquals(false, assertNotNull(MutFlow.underTest { runs.get(tenant.id, run.id) }).partial)
    }

    // --- the config

    private val defaultQ by lazy { q() }

    private fun violations(config: String): List<ConfigViolation> {
        val refused = assertFailsWith<RestoreConfigInvalid> { start(defaultQ, draft(a, config)) }
        return refused.violations
    }

    @Test
    fun `a secret the target agent does not have is refused at password_ref`() {
        val config = """{"host":"h","user":"r","password_ref":"only-on-a","database":"x"}"""

        val unknown = ConfigViolation("config/password_ref", """unknown secret "only-on-a"""")
        assertEquals(listOf(unknown), violations(config))
    }

    @Test
    fun `a password in the request is refused, however it is named`() {
        for (name in listOf("password", "pgpassword", "secret")) {
            val config = """{"host":"h","user":"r","password_ref":"pg-app","database":"x","$name":"hunter2"}"""
            val refused = violations(config)
            val extra = ConfigViolation("config", """violates "additionalProperties" of the schema""")
            assertEquals(listOf(extra), refused, name)
            assertTrue(refused.none { "hunter2" in it.message }, "the value is not repeated")
        }
        assertEquals(0, restoreRuns())
    }

    @Test
    fun `a missing database, one of 64 bytes and a global flag that is no boolean are refused at their fields`() {
        assertEquals(
            listOf(ConfigViolation("config/database", """violates "required" of the schema""")),
            violations("""{"host":"h","user":"r","password_ref":"pg-app"}"""),
        )
        val tooLong = """{"host":"h","user":"r","password_ref":"pg-app","database":"${"a".repeat(64)}"}"""
        val maxLength = ConfigViolation("config/database", """violates "maxLength" of the schema""")
        assertEquals(listOf(maxLength), violations(tooLong))
        val globals = """{"host":"h","user":"r","password_ref":"pg-app","database":"x","restore_globals":"yes"}"""
        val type = ConfigViolation("config/restore_globals", """violates "type" of the schema""")
        assertEquals(listOf(type), violations(globals))
    }

    // --- the rest of what the restore of a database reaches

    private fun newName(database: String): String {
        val config = """{"host":"h","database":"$database","user":"b","password_ref":"pg-app"}"""
        val defaults = options(q(config)).targets.single().defaults
        return JSON.readTree(defaults).path("database").asString()
    }

    @Test
    fun `a name that just fits is kept whole, one byte more is cut`() {
        val room = 63 - "_restored_20261011".length
        assertEquals("a".repeat(room) + "_restored_20261011", newName("a".repeat(room)))
    }

    @Test
    fun `a name one byte too long loses that byte`() {
        val room = 63 - "_restored_20261011".length
        assertEquals("a".repeat(room) + "_restored_20261011", newName("a".repeat(room + 1)))
    }

    @Test
    fun `a database target has no restore directory, a files target has one, the source agent is told`() {
        val s = q()
        val b = agent("db2")
        val files = scene.source("etc data")
        scene.snapshot(files)

        val targets = options(s).targets

        assertEquals(listOf(a, b), targets.map { it.agentId })
        assertEquals(listOf(true, false), targets.map { it.sourceAgent })
        assertEquals(listOf(null, null), targets.map { it.restoreDir })
        val filesTarget = MutFlow.underTest { restores.options(tenant.id, files, RestoreScene.SF) }.targets.first()
        assertEquals(RestoreScene.DIR, filesTarget.restoreDir)
    }

    @Test
    fun `what the agent reported of a database restore reads back with the run`() {
        val s = q()
        val run = start(s, draft(a))
        jdbc.update(
            "update run_steps set output = ?::jsonb where run_id = ?",
            """{"kind":"restore","database":"app_restored_20261011","tablesRestored":3,"rolesRestored":1}""",
            run.id,
        )

        val got = assertNotNull(MutFlow.underTest { runs.get(tenant.id, run.id) })

        assertEquals(
            RestoreResult(null, null, null, "app_restored_20261011", 3, 1, null, null),
            got.steps.single().restore,
        )
        assertNull(got.steps.single().backup)
        assertEquals(false, got.partial)
    }

    @Test
    fun `a restore schema that is no schema or loads a resource refuses every config`() {
        val s = q()
        val broken = ConfigViolation("config", "the plugin's config schema is not a valid JSON Schema")
        val schemas =
            listOf(
                """{"${'$'}ref":"classpath:schemas/string-only.json"}""",
                """{"properties":{"a":{"pattern":"("}}}""",
            )
        for (schema in schemas) {
            jdbc.update(
                "update agent_plugins set restore_config_schema = ?::jsonb where agent_id = ? and name = 'postgresql'",
                schema,
                a,
            )

            val refused = assertFailsWith<RestoreConfigInvalid> { start(s, draft(a)) }
            assertEquals(listOf(broken), refused.violations, schema)
        }
    }

    @Test
    fun `a revoked agent and a verifier are refused as targets of a database restore`() {
        val s = q()
        val revoked = agent("gone").also { jdbc.update("update agents set revoked_at = now() where id = ?", it) }
        val verifier = scene.agent("ver", plugins = listOf("postgresql"), role = "verifier")

        assertEquals(revoked, assertFailsWith<AgentRevoked> { start(s, draft(revoked)) }.agentId)
        val mismatch = assertFailsWith<AgentRoleMismatch> { start(s, draft(verifier)) }
        assertEquals(verifier, mismatch.agentId)
        assertEquals(Action.RESTORE, mismatch.action)
        assertEquals(0, restoreRuns())
    }

    @Test
    fun `a source may be asked to back up, restore and run, a verifier only to verify`() {
        for (action in Action.entries) {
            val source = MutFlow.underTest { AgentRole.SOURCE.allows(action) }
            val verifier = MutFlow.underTest { AgentRole.VERIFIER.allows(action) }
            assertEquals(action != Action.VERIFY, source, "source $action")
            assertEquals(action == Action.VERIFY, verifier, "verifier $action")
        }
    }

    @Test
    fun `only the system tenant is root`() {
        val bridge = HibernateTenantBridge { tenant.id }
        assertEquals(false, MutFlow.underTest { bridge.isRoot(tenant.id) })
        assertEquals(true, MutFlow.underTest { bridge.isRoot(HibernateTenantBridge.SYSTEM_TENANT_ID) })
    }
}
