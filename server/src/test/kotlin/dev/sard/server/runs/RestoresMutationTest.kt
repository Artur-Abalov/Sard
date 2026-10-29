// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.runs

import dev.sard.server.TestcontainersConfiguration
import dev.sard.server.persistence.TenantSessions
import dev.sard.server.persistence.UuidV7
import dev.sard.server.pki.MovableClock
import dev.sard.server.role.AgentRole
import dev.sard.server.runs.RestoreScene.Companion.DESTINATION
import dev.sard.server.runs.RestoreScene.Companion.DIR
import dev.sard.server.runs.RestoreScene.Companion.RESTORE_SCHEMA
import dev.sard.server.runs.RestoreScene.Companion.RID
import dev.sard.server.runs.RestoreScene.Companion.RID2
import dev.sard.server.runs.RestoreScene.Companion.SF
import dev.sard.server.runs.RestoreScene.Companion.SF_AT
import dev.sard.server.runs.RestoreScene.Companion.SF_BYTES
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import org.junit.jupiter.api.Tag
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.context.annotation.Import
import org.springframework.jdbc.core.JdbcTemplate
import tools.jackson.databind.json.JsonMapper
import java.security.SecureRandom
import java.sql.Timestamp
import java.time.Duration
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
private const val REQUIRED = """violates "required" of the schema"""

/**
 * Mutation tests of the restore service over the database (R2a, КВ1-КВ4, ВК30-ВК35): which agents restore a
 * snapshot and which cannot, what a request would do, the order in which a request is refused, and the run a
 * restore makes.
 */
@Tag("mutflow-isolated")
@MutFlowTest(excludeTargets = [TenantSessions::class]) // TenantSessionsTest mutates it
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.NONE,
    properties = ["spring.grpc.server.port=0"],
)
@Import(TestcontainersConfiguration::class, RunsTestConfiguration::class)
class RestoresMutationTest(
    @Autowired private val restores: Restores,
    @Autowired private val sources: Sources,
    @Autowired private val runs: Runs,
    @Autowired private val timeouts: TimeoutSettings,
    @Autowired private val sessions: TenantSessions,
    @Autowired private val queued: RecordingStepsQueued,
    @Autowired private val clock: MovableClock,
    @Autowired private val jdbc: JdbcTemplate,
) {
    private val scene = RestoreScene(jdbc, sources, runs, queued, RunsTenant(jdbc), RunsTenant(jdbc))
    private val tenant = scene.tenant
    private val other = scene.other
    private val a get() = scene.a

    @BeforeTest
    fun `agent A holds the repository`() {
        clock.now = RUNS_NOW
        scene.create()
    }

    @AfterTest
    fun `drop both tenants`() = scene.drop()

    private fun agent(
        host: String,
        name: String = "copy",
        id: String? = RID,
        dir: String? = DIR,
        plugins: List<String> = listOf("files"),
        restore: Boolean = true,
        revoked: Boolean = false,
        role: String = "source",
    ) = scene.agent(host, name, id, dir, plugins, restore, revoked, role)

    private fun files(
        agent: UUID,
        plugin: String = "files",
        restore: Boolean = true,
    ) = scene.files(agent, plugin, restore)

    private fun source(
        name: String = "etc data",
        plugin: String = "files",
        config: String = """{"paths":["/etc"]}""",
    ) = scene.source(name, plugin, config)

    private fun snapshot(
        source: UUID,
        snapshot: String = SF,
        partial: Boolean = false,
        forgotten: Boolean = false,
    ) = scene.snapshot(source, snapshot, partial, forgotten)

    private fun options(source: UUID) = MutFlow.underTest { restores.options(tenant.id, source, SF) }

    private fun draft(
        agent: Any?,
        config: String = DESTINATION,
    ) = RestoreDraft(agent?.toString(), config)

    private fun preview(
        source: UUID,
        draft: RestoreDraft,
        snapshot: String = SF,
    ) = MutFlow.underTest { restores.preview(tenant.id, source, snapshot, draft) }

    private fun start(
        source: UUID,
        draft: RestoreDraft,
        snapshot: String = SF,
    ) = MutFlow.underTest { restores.start(tenant.id, source, snapshot, draft) }

    private fun restoreRuns() = jdbc.queryForObject("select count(*) from runs where kind = 'restore'", Int::class.java)

    // --- the options

    @Test
    fun `the options name the snapshot, the agents that restore it first the source's own, and how to attach`() {
        val s = source()
        snapshot(s)
        val x = agent("db3")
        val y = agent("db2")
        val z = agent("db2")
        val (first, second) = listOf(y, z).sorted()

        val got = options(s)

        assertEquals(RestoreSnapshot(SF, SF_AT, SF_BYTES, "main", "etc data", "files"), got.snapshot)
        assertEquals(listOf(a, first, second, x), got.targets.map { it.agentId })
        assertEquals(listOf(true, false, false, false), got.targets.map { it.sourceAgent })
        assertEquals(listOf("db9", "db2", "db2", "db3"), got.targets.map { it.hostname })
        assertEquals(listOf("main", "copy", "copy", "copy"), got.targets.map { it.repositoryName })
        assertEquals(listOf(DIR, DIR, DIR, DIR), got.targets.map { it.restoreDir })
        val schema = JSON.readTree(RESTORE_SCHEMA)
        assertEquals(List(4) { schema }, got.targets.map { JSON.readTree(it.restoreConfigSchema) })
        assertEquals(List(4) { DESTINATION }, got.targets.map { it.defaults })
        assertEquals("main", got.attachRepositoryName)
        assertEquals(
            "sudo sard-agent repo add main <адрес хранилища> --password-from-file <файл с копией пароля>",
            got.attachCommand,
        )
        assertEquals(emptyList(), got.ineligible)
    }

    @Test
    fun `an agent that holds the repository but cannot restore is named with the reason, the others are left out`() {
        val s = source()
        snapshot(s)
        val noPlugin = agent("zulu", plugins = listOf("postgresql"))
        val outdated = agent("alpha", restore = false)
        val noDir = agent("mike", dir = null)
        agent("revoked", revoked = true)
        agent("elsewhere", name = "other", id = RID2)
        agent("unannounced", id = null)

        val got = options(s)

        assertEquals(listOf(a), got.targets.map { it.agentId })
        assertEquals(
            listOf(
                IneligibleAgent(outdated, "alpha", IneligibleReason.AGENT_OUTDATED),
                IneligibleAgent(noDir, "mike", IneligibleReason.AGENT_OUTDATED),
                IneligibleAgent(noPlugin, "zulu", IneligibleReason.PLUGIN_MISSING),
            ),
            got.ineligible,
        )
    }

    @Test
    fun `the options of a snapshot are found for a deleted source too, and only for a snapshot of that source`() {
        val s = source()
        snapshot(s)
        val deleted = source("old")
        snapshot(deleted, "d".repeat(64))
        jdbc.update("update sources set deleted_at = ? where id = ?", Timestamp.from(RUNS_NOW), deleted)

        val oldOptions = MutFlow.underTest { restores.options(tenant.id, deleted, "d".repeat(64)) }
        assertEquals("old", oldOptions.snapshot.sourceName)
        assertFailsWith<SnapshotNotFound> { MutFlow.underTest { restores.options(tenant.id, s, "d".repeat(64)) } }
        assertFailsWith<SnapshotNotFound> { MutFlow.underTest { restores.options(tenant.id, UUID.randomUUID(), SF) } }
        assertFailsWith<SnapshotNotFound> { MutFlow.underTest { restores.options(other.id, s, SF) } }
    }

    // --- the preview

    @Test
    fun `a preview says where the restore goes, which paths and what it will not do, and creates nothing`() {
        val s = source()
        snapshot(s)
        val b = agent("db2")
        jdbc.update("update agents set restore_dir = '/srv/restore/' where id = ?", b)

        val got = preview(s, draft(b, """{"destination":"x/y","paths":["/etc/hosts","/srv"]}"""))

        assertEquals(
            RestorePreview(
                "etc data",
                SF,
                SF_AT,
                SF_BYTES,
                b,
                "db2",
                "/srv/restore/x/y",
                listOf("/etc/hosts", "/srv"),
                false,
                listOf(RestoreWarning.OWNERS_NOT_RESTORED),
            ),
            got,
        )
        assertEquals(0, restoreRuns())
        assertTrue(queued.calls.isEmpty())
        assertEquals(emptyList(), preview(s, draft(a)).paths)
        assertEquals("$DIR/etc%20data/20261010T030000Z", preview(s, draft(a)).target)
    }

    // --- the order of the refusals (ВК33)

    private fun refusedBy(
        source: UUID,
        draft: RestoreDraft,
        snapshot: String = SF,
    ): Exception {
        val byPreview = assertFailsWith<Exception> { preview(source, draft, snapshot) }
        val byStart = assertFailsWith<Exception> { start(source, draft, snapshot) }
        assertEquals(byPreview::class, byStart::class, "preview and start refuse alike")
        assertEquals(0, restoreRuns())
        assertTrue(queued.calls.isEmpty())
        return byStart
    }

    @Test
    fun `a snapshot the server does not know is not found, before anything else is judged`() {
        val s = source()
        snapshot(s)

        refusedBy(s, draft(null), "c".repeat(64)).let { assertTrue(it is SnapshotNotFound, "$it") }
        refusedBy(UUID.randomUUID(), draft(a)).let { assertTrue(it is SnapshotNotFound, "$it") }
    }

    @Test
    fun `a snapshot the console does not restore is refused with its reason, before the agent is judged`() {
        val partial = source("partial")
        snapshot(partial, partial = true)
        val forgotten = source("forgotten")
        snapshot(forgotten, "e".repeat(64), forgotten = true)
        val both = source("both")
        snapshot(both, "f".repeat(64), partial = true, forgotten = true)
        files(a, "mysql", restore = false)
        val database = source("database", "mysql")
        snapshot(database, "9".repeat(64))

        val reasons =
            listOf(partial to SF, forgotten to "e".repeat(64), both to "f".repeat(64), database to "9".repeat(64))
                .map { (s, snap) -> (refusedBy(s, draft("not an agent"), snap) as SnapshotNotRestorable).reason }

        assertEquals(
            listOf(
                RestoreBlock.PARTIAL,
                RestoreBlock.FORGOTTEN,
                RestoreBlock.PARTIAL,
                RestoreBlock.PLUGIN_NOT_SUPPORTED,
            ),
            reasons,
        )
    }

    @Test
    fun `an agent that is no agent of the tenant is refused at agentId`() {
        val s = source()
        snapshot(s)

        for (agent in listOf<Any?>(null, "", "not-a-uuid", UUID.randomUUID(), other.agentId)) {
            assertTrue(refusedBy(s, draft(agent)) is RestoreAgentInvalid, "agent $agent")
        }
    }

    @Test
    fun `a revoked agent is refused before its repository and plugin are judged`() {
        val s = source()
        snapshot(s)
        val revoked = agent("gone", name = "other", id = RID2, revoked = true)

        assertEquals(revoked, (refusedBy(s, draft(revoked)) as AgentRevoked).agentId)
    }

    @Test
    fun `an agent without the repository of the snapshot is refused naming it, before the config is judged`() {
        val s = source()
        snapshot(s)
        val elsewhere = agent("db3", name = "other", id = RID2)

        assertEquals("main", (refusedBy(s, draft(elsewhere, "{}")) as UnknownRepository).repositoryName)
    }

    @Test
    fun `an agent that cannot restore the plugin from the console is refused naming the plugin`() {
        val s = source()
        snapshot(s)
        val noPlugin = agent("db3", plugins = listOf("postgresql"))
        val outdated = agent("db4", restore = false)
        val noDir = agent("db5", dir = null)

        for (agent in listOf(noPlugin, outdated, noDir)) {
            assertEquals("files", (refusedBy(s, draft(agent, "{}")) as UnknownPlugin).plugin, "agent $agent")
        }
    }

    @Test
    fun `a config the restore schema of the agent refuses is refused at the field, secrets by name`() {
        val s = source()
        snapshot(s)

        fun violations(config: String) = (refusedBy(s, draft(a, config)) as RestoreConfigInvalid).violations

        assertEquals(listOf(ConfigViolation("config/destination", REQUIRED)), violations("{}"))
        assertEquals(
            listOf(ConfigViolation("config/destination", """violates "minLength" of the schema""")),
            violations("""{"destination":""}"""),
        )
        assertEquals(
            listOf(ConfigViolation("config", """violates "additionalProperties" of the schema""")),
            violations("""{"destination":"x","overwrite":true}"""),
        )
        assertEquals(
            listOf(ConfigViolation("config/token", """unknown secret "nope"""")),
            violations("""{"destination":"x","token":"nope"}"""),
        )
        assertEquals(
            listOf(ConfigViolation("config/paths/1", """violates "type" of the schema""")),
            violations("""{"destination":"x","paths":["/a",3]}"""),
        )
        assertEquals("$DIR/x/y", preview(s, draft(a, """{"destination":"x/y","token":"pg-app"}""")).target)
    }

    @Test
    fun `a restore schema that is no schema or loads a resource from outside refuses every config`() {
        val s = source()
        snapshot(s)
        val broken = ConfigViolation("config", "the plugin's config schema is not a valid JSON Schema")

        val schemas =
            listOf(
                """{"${'$'}ref":"classpath:schemas/string-only.json"}""",
                """{"properties":{"a":{"pattern":"("}}}""",
            )
        for (schema in schemas) {
            jdbc.update(
                "update agent_plugins set restore_config_schema = ?::jsonb where agent_id = ? and name = 'files'",
                schema,
                a,
            )

            val refused = refusedBy(s, draft(a, """{"a":"x"}""")) as RestoreConfigInvalid
            assertEquals(listOf(broken), refused.violations, schema)
        }
    }

    @Test
    fun `a verifier agent is no restore target and is refused as one, after the revoked check`() {
        val s = source()
        snapshot(s)
        val verifier = agent("verifier", role = "verifier")
        val revoked = agent("revoked-verifier", role = "verifier", revoked = true)

        val got = options(s)

        assertEquals(listOf(a), got.targets.map { it.agentId })
        assertEquals(emptyList(), got.ineligible)
        val refused = refusedBy(s, draft(verifier)) as AgentRoleMismatch
        assertEquals(verifier, refused.agentId)
        assertEquals(Action.RESTORE, refused.action)
        assertEquals(revoked, (refusedBy(s, draft(revoked)) as AgentRevoked).agentId)
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

    // --- the restore run

    @Test
    fun `a restore is a manual run of its own kind with one restore step on the chosen agent`() {
        val s = source()
        snapshot(s)
        val b = agent("db2")
        val config = """{"destination":"x/y","paths":["/etc"]}"""

        val run = start(s, draft(b, config))

        assertEquals(RunKind.RESTORE, run.kind)
        assertEquals(Trigger.MANUAL, run.trigger)
        assertEquals(RunState.QUEUED, run.status)
        assertEquals(false, run.partial, "a restore run backs nothing up, so it is not partial")
        assertEquals("admin", run.requestedBy)
        assertEquals(s, run.sourceId)
        assertEquals("etc data", run.sourceName)
        assertEquals(b, run.agentId)
        assertEquals(RUNS_NOW, run.queuedAt)
        assertEquals(RestoreInfo(SF, SF_AT, "copy", config, "$DIR/x/y"), run.restore)
        val step = run.steps.single()
        assertEquals(0, step.ordinal)
        assertEquals(Action.RESTORE, step.action)
        assertEquals(StepState.QUEUED, step.status)
        assertEquals(b, step.agentId)
        assertEquals(s, step.sourceId)
        assertEquals("files", step.plugin)
        assertEquals("copy", step.repositoryName)
        assertEquals(config, step.config)
        assertEquals(SF, step.snapshotId)
        assertEquals(RUNS_NOW, step.queuedAt)
        assertEquals(listOf(tenant.id to b), queued.calls.toList())
        val stored = assertNotNull(MutFlow.underTest { runs.get(tenant.id, run.id) })
        assertEquals(RunKind.RESTORE, stored.kind)
        assertEquals("admin", stored.requestedBy)
        assertEquals("$DIR/x/y", stored.restore?.target)
        assertEquals("copy", stored.steps.single().repositoryName)
        assertEquals(false, stored.partial)
        assertEquals(1, restoreRuns())
    }

    // J1c: Ожидание не дольше потолка (восстановление), закрывает OQ-233
    @Test
    fun `a restore waits for an offline agent for a day, and keeps the deadline`() {
        val s = source()
        snapshot(s)

        val run = start(s, draft(a))

        assertEquals(RUNS_NOW.plusSeconds(24 * 3600), run.agentWaitUntil)
        assertEquals(RUNS_NOW.plusSeconds(24 * 3600), runs.get(tenant.id, run.id)!!.agentWaitUntil)
    }

    // J1c: the answer to the start of a restore says the same as a later read
    @Test
    fun `a restore started for an offline agent is waiting for the agent at once`() {
        val s = source()
        snapshot(s)
        val offline =
            Restores(
                sessions,
                clock,
                UuidV7(clock, SecureRandom()),
                queued,
                Duration.ofHours(24),
                { false },
            )

        val run = MutFlow.underTest { offline.start(tenant.id, s, SF, draft(a)) }

        assertEquals(RunOutcome.WAITING_FOR_AGENT, run.outcome)
    }

    // J1b: the step of a restore gets the source's restore timeout, or the tenant's default
    @Test
    fun `a restore step takes the restore timeout of its source, else the tenant's default, and keeps it`() {
        val s = source()
        snapshot(s)
        assertEquals(720 * 60L, start(s, draft(a)).steps.single().timeoutSeconds)

        jdbc.update("update sources set restore_timeout_minutes = 45 where id = ?", s)
        val own = start(s, draft(a))
        assertEquals(45 * 60L, own.steps.single().timeoutSeconds)

        jdbc.update("update sources set restore_timeout_minutes = 30 where id = ?", s)
        timeouts.set(tenant.id, TimeoutDefaults(60, 90))
        assertEquals(
            45 * 60L,
            runs
                .get(tenant.id, own.id)!!
                .steps
                .single()
                .timeoutSeconds,
        )
        assertEquals(30 * 60L, start(s, draft(a)).steps.single().timeoutSeconds)
        jdbc.update("update sources set restore_timeout_minutes = null where id = ?", s)
        assertEquals(90 * 60L, start(s, draft(a)).steps.single().timeoutSeconds)
    }

    @Test
    fun `a restore neither blocks a backup of its source nor waits for one, and several may be queued`() {
        val s = source()
        snapshot(s)

        val backup = runs.start(tenant.id, s)
        queued.calls.clear()
        val first = start(s, draft(a))
        val second = start(s, draft(a))

        assertEquals(RunKind.BACKUP, backup.kind)
        assertEquals(setOf(RunState.QUEUED), setOf(backup.status, first.status, second.status))
        assertEquals(2, restoreRuns())
        assertEquals(2, queued.calls.size)
    }

    @Test
    fun `what the agent reported of a restore reads back with the run`() {
        val s = source()
        snapshot(s)
        val run = start(s, draft(a))
        jdbc.update(
            "update run_steps set output = ?::jsonb where run_id = ?",
            """{"kind":"restore","target":"$DIR/x","filesRestored":2,"bytesRestored":10,"ownersRestored":false}""",
            run.id,
        )

        val got = assertNotNull(MutFlow.underTest { runs.get(tenant.id, run.id) })

        assertEquals(RestoreResult("$DIR/x", 2, 10, null, null, null, false, null), got.steps.single().restore)
        assertEquals(false, got.partial)
        assertNull(got.steps.single().backup)
    }

    // J1c: a queued run knows whether its agent is online, a run in flight does not ask
    @Test
    fun `a run view of a queued run says its agent is offline and when its wait ends, one in flight does not`() {
        val waiting = source("waiting")
        val active = source("active")

        assertWaitingAndActiveViews(runs, jdbc, tenant.id, waiting, active)
    }
}
