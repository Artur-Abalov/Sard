// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import dev.sard.server.TestcontainersConfiguration
import dev.sard.server.extension.TenantResolver
import dev.sard.server.fleet.AgentPresence
import dev.sard.server.pki.MovableClock
import dev.sard.server.runs.RUNS_NOW
import dev.sard.server.runs.RecordingStepsQueued
import dev.sard.server.runs.RestoreScene
import dev.sard.server.runs.RestoreScene.Companion.DIR
import dev.sard.server.runs.RestoreScene.Companion.RESTORE_SCHEMA
import dev.sard.server.runs.RestoreScene.Companion.RID
import dev.sard.server.runs.RestoreScene.Companion.RID2
import dev.sard.server.runs.RestoreScene.Companion.SF
import dev.sard.server.runs.RestoreScene.Companion.SF_AT
import dev.sard.server.runs.RestoreScene.Companion.SF_BYTES
import dev.sard.server.runs.Restores
import dev.sard.server.runs.Runs
import dev.sard.server.runs.RunsTenant
import dev.sard.server.runs.RunsTestConfiguration
import dev.sard.server.runs.Sources
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import org.junit.jupiter.api.Tag
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.context.annotation.Import
import org.springframework.jdbc.core.JdbcTemplate
import tools.jackson.core.type.TypeReference
import tools.jackson.databind.ObjectMapper
import java.util.UUID
import kotlin.test.AfterTest
import kotlin.test.BeforeTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertTrue

private class Online(
    var ids: Set<UUID> = emptySet(),
) : AgentPresence {
    override fun online(agentId: UUID) = agentId in ids

    override fun onlineIds() = ids

    override fun disconnectRevoked(agentId: UUID) = Unit
}

/** R2a: the restore endpoints as the contract has them, over the real service (КВ1-КВ4). */
@Tag("mutflow-isolated")
@MutFlowTest(includeTargets = [RestoresApiImpl::class])
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.NONE,
    properties = ["spring.grpc.server.port=0"],
)
@Import(TestcontainersConfiguration::class, RunsTestConfiguration::class)
class RestoresApiImplTest(
    @Autowired private val restores: Restores,
    @Autowired private val sources: Sources,
    @Autowired private val runs: Runs,
    @Autowired private val queued: RecordingStepsQueued,
    @Autowired private val clock: MovableClock,
    @Autowired private val mapper: ObjectMapper,
    @Autowired private val jdbc: JdbcTemplate,
) {
    private val scene = RestoreScene(jdbc, sources, runs, queued, RunsTenant(jdbc), RunsTenant(jdbc))
    private val presence = Online()
    private val api = RestoresApiImpl(restores, presence, TenantResolver { scene.tenant.id }, mapper)
    private val a get() = scene.a

    @BeforeTest
    fun `agent A holds the repository`() {
        clock.now = RUNS_NOW
        scene.create()
    }

    @AfterTest
    fun `drop both tenants`() = scene.drop()

    private fun snapshotOfSource(name: String = "etc data"): UUID = scene.source(name).also { scene.snapshot(it) }

    private fun request(
        agent: Any?,
        config: Map<String, Any?>? = mapOf("destination" to "etc%20data/20261010T030000Z"),
    ) = RestoreRequest(agent?.toString(), config)

    // --- the options

    @Test
    fun `the options are as the contract has them, with the presence of each agent`() {
        val s = snapshotOfSource()
        val b = scene.agent("db2")
        val outdated = scene.agent("alpha", restore = false)
        val missing = scene.agent("zulu", plugins = listOf("postgresql"))
        presence.ids = setOf(b)

        val got = MutFlow.underTest { api.restoreOptions(s, SF) }

        assertEquals(RestoreSnapshot(SF, SF_AT, SF_BYTES, "main", "etc data", "files"), got.snapshot)
        val schema: Map<String, Any?> = mapper.readValue(RESTORE_SCHEMA, object : TypeReference<Map<String, Any?>>() {})
        val defaults = mapOf("destination" to "etc%20data/20261010T030000Z")
        assertEquals(
            listOf(
                RestoreTarget(a, "db9", false, true, "main", DIR, schema, defaults),
                RestoreTarget(b, "db2", true, false, "copy", DIR, schema, defaults),
            ),
            got.targets,
        )
        assertEquals(
            listOf(
                IneligibleAgent(outdated, "alpha", IneligibleReason.AGENT_OUTDATED),
                IneligibleAgent(missing, "zulu", IneligibleReason.PLUGIN_MISSING),
            ),
            got.ineligible,
        )
        assertEquals(
            AttachHint(
                "main",
                "sudo sard-agent repo add main <адрес хранилища> --password-from-file <файл с копией пароля>",
            ),
            got.attachHint,
        )
    }

    // --- the preview

    @Test
    fun `a preview of an online agent warns of the owners only, of an offline one first of the agent`() {
        val s = snapshotOfSource()
        presence.ids = setOf(a)

        val online = MutFlow.underTest { api.previewRestore(s, SF, request(a)) }

        assertEquals(
            RestorePreview(
                "etc data",
                SF,
                SF_AT,
                SF_BYTES,
                a,
                "db9",
                true,
                "$DIR/etc%20data/20261010T030000Z",
                emptyList(),
                false,
                listOf(RestoreWarningCode.OWNERS_NOT_RESTORED),
            ),
            online,
        )

        presence.ids = emptySet()
        val paths = mapOf("destination" to "x", "paths" to listOf("/etc"))
        val offline = MutFlow.underTest { api.previewRestore(s, SF, request(a, paths)) }

        assertEquals(false, offline.agentOnline)
        assertEquals(listOf(RestoreWarningCode.AGENT_OFFLINE, RestoreWarningCode.OWNERS_NOT_RESTORED), offline.warnings)
        assertEquals(listOf("/etc"), offline.paths)
        assertEquals("$DIR/x", offline.target)
    }

    @Test
    fun `a request without a config is a request with an empty one, which the schema refuses at the field`() {
        val s = snapshotOfSource()

        val refused =
            assertFailsWith<RequestRefused> { MutFlow.underTest { api.previewRestore(s, SF, request(a, null)) } }

        assertEquals(ErrorCode.VALIDATION_FAILED, refused.code)
        assertEquals(listOf(FieldError("config/destination", """violates "required" of the schema""")), refused.errors)
    }

    @Test
    fun `a config of 64 KiB is read, a byte more is refused at config`() {
        val s = snapshotOfSource()

        fun config(size: Int): Map<String, Any?> {
            val base = mapper.writeValueAsString(mapOf("destination" to "x", "paths" to listOf("")))
            return mapOf("destination" to "x", "paths" to listOf("p".repeat(size - base.length)))
        }

        val ok = MutFlow.underTest { api.previewRestore(s, SF, request(a, config(64 * 1024))) }
        val tooBig =
            assertFailsWith<RequestInvalid> {
                MutFlow.underTest { api.previewRestore(s, SF, request(a, config(64 * 1024 + 1))) }
            }

        assertEquals(1, ok.paths.size)
        assertEquals("config", tooBig.field)
        assertEquals("must be at most 64 KiB", tooBig.message)
    }

    // --- the refusals as problems

    @Test
    fun `each refusal of the domain is the problem of the contract`() {
        val s = snapshotOfSource()
        val partial = scene.source("partial").also { scene.snapshot(it, "e".repeat(64), partial = true) }
        val revoked = scene.agent("gone", revoked = true)
        val elsewhere = scene.agent("db3", name = "other", id = RID2)
        val outdated = scene.agent("db4", restore = false)

        fun refusal(
            source: UUID = s,
            snapshot: String = SF,
            agent: Any? = a,
            config: Map<String, Any?>? = mapOf("destination" to "x"),
        ): Exception {
            val asked = request(agent, config)
            val byPreview =
                assertFailsWith<Exception> { MutFlow.underTest { api.previewRestore(source, snapshot, asked) } }
            val byStart = assertFailsWith<Exception> { MutFlow.underTest { api.startRestore(source, snapshot, asked) } }
            assertEquals(byPreview::class, byStart::class)
            return byStart
        }

        assertTrue(refusal(snapshot = "c".repeat(64)) is ResourceNotFound)
        assertFailsWith<ResourceNotFound> { MutFlow.underTest { api.restoreOptions(s, "c".repeat(64)) } }
        val notRestorable = refusal(partial, "e".repeat(64)) as RequestRefused
        assertEquals(ErrorCode.SNAPSHOT_NOT_RESTORABLE, notRestorable.code)
        assertEquals(
            listOf(FieldError("snapshotId", "cannot be restored from the console: partial")),
            notRestorable.errors,
        )
        val agent = refusal(agent = "nobody") as RequestInvalid
        assertEquals("agentId", agent.field)
        assertEquals("must name an agent of this tenant that can restore the snapshot", agent.message)
        assertEquals(ErrorCode.AGENT_REVOKED, (refusal(agent = revoked) as RunRefused).code)
        assertEquals(ErrorCode.UNKNOWN_REPOSITORY, (refusal(agent = elsewhere) as RunRefused).code)
        assertEquals(ErrorCode.UNKNOWN_PLUGIN, (refusal(agent = outdated) as RunRefused).code)
        val config = refusal(config = mapOf("destination" to "x", "token" to "nope")) as RequestRefused
        assertEquals(ErrorCode.VALIDATION_FAILED, config.code)
        assertEquals(listOf(FieldError("config/token", """unknown secret "nope"""")), config.errors)
        assertEquals(0, jdbc.queryForObject("select count(*) from runs where kind = 'restore'", Int::class.java))
    }

    // --- the restore

    @Test
    fun `a restore is the run of the contract`() {
        val s = snapshotOfSource()
        val b = scene.agent("db2")

        val run = MutFlow.underTest { api.startRestore(s, SF, request(b)) }

        assertEquals(RunKind.RESTORE, run.kind)
        assertEquals(RunStatus.QUEUED, run.status)
        assertEquals(RunTrigger.MANUAL, run.trigger)
        assertEquals(b, run.agentId)
        assertEquals(s, run.sourceId)
        assertEquals("etc%20data/20261010T030000Z", run.restore?.config?.get("destination"))
        assertEquals("$DIR/etc%20data/20261010T030000Z", run.restore?.target)
        assertEquals("copy", run.restore?.repositoryName)
        assertEquals(SF, run.restore?.snapshotId)
    }
}
