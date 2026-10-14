// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.e2e

import org.junit.jupiter.api.extension.RegisterExtension
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNotNull
import kotlin.test.assertTrue

/**
 * R2a, restore from the console with real containers (docs/specs/server/console-restore.feature, rule "Восстановление
 * files"; docs/specs/agent/console-restore.feature): a backup through the console, then the console's restore of its
 * snapshot into a new directory inside the agent's `restore.dir`, byte for byte, owned by the service user, with
 * nothing left in `.sard-staging`; and a restore that cannot finish leaves what was there as it was.
 */
class ConsoleRestoreTest {
    @Test
    fun `a snapshot restores byte for byte into a new directory of the restore dir, nothing left in staging`() {
        val (agent, tree, snapshot, sourceId) = backedUp("restore-ok")

        val started = Backups.restore(sard, sourceId, snapshot, agent.agentId, """{"destination":"copy"}""")
        val step = Backups.awaitFinished(sard, started.stepId)

        assertEquals("succeeded", step.status, step.message)
        assertEquals("succeeded", Backups.runStatus(sard, started.runId))
        assertEquals("$RESTORE_DIR/copy", Backups.outputField(sard, started.stepId, "target"))
        assertEquals(emptyList(), TreeDiff.of(tree.expected, agent.host.tree("$RESTORE_DIR/copy$DATA")))
        assertEquals("$AGENT_UID:$AGENT_UID 700", agent.host.stat("$RESTORE_DIR/copy"))
        assertEquals(emptyMap(), agent.host.tree("$RESTORE_DIR/.sard-staging"), "something is left in the staging directory")
    }

    @Test
    fun `a destination that is not empty fails the step, restores nothing and leaves the directory as it was`() {
        val (agent, _, snapshot, sourceId) = backedUp("restore-busy")
        agent.host.put("$RESTORE_DIR/busy", TarFiles(listOf(TarFiles.Entry("", null, TarFiles.OWNER_DIR), TarFiles.Entry("keep.txt", "kept".toByteArray(), TarFiles.READABLE))))

        val started = Backups.restore(sard, sourceId, snapshot, agent.agentId, """{"destination":"busy"}""")
        val step = Backups.awaitFinished(sard, started.stepId)

        assertEquals("failed", step.status)
        assertEquals("failed", Backups.runStatus(sard, started.runId))
        assertNotNull(step.message)
        assertEquals(listOf("kept"), agent.host.tree("$RESTORE_DIR/busy").filterKeys { it == "keep.txt" }.values.map { String(it!!) })
        assertFalse(agent.host.exists("$RESTORE_DIR/busy$DATA"), "the snapshot was restored over a directory that was not empty")
        assertTrue(agent.host.tree("$RESTORE_DIR/.sard-staging").isEmpty(), "something is left in the staging directory")
    }

    @Test
    fun `a destination that leaves the restore directory is refused before anything is queued`() {
        val (agent, _, snapshot, sourceId) = backedUp("restore-escape")

        val answer = SardApi(sard).post("/api/v1/sources/$sourceId/snapshots/$snapshot/restores", """{"agentId":"${agent.agentId}","config":{"destination":"../etc"}}""")

        assertEquals(422, answer.status)
        assertEquals("validation_failed", answer.field("code"))
        assertEquals("config/destination", answer.field("field"))
    }

    private data class BackedUp(
        val agent: EnrolledAgent,
        val tree: SourceTree,
        val snapshotId: String,
        val sourceId: java.util.UUID,
    )

    /** An agent with a repository, [DATA] backed up through the console; its snapshot's restic id. */
    private fun backedUp(hostname: String): BackedUp {
        val agent = AgentEnroller.enroll(sard, EnrollmentTokens.create(sard), hostname = hostname, local = LOCAL)
        val tree = SourceTree.generate(Chain.SEED)
        agent.host.put(DATA, tree.files())
        Chain.ready(sard, agent)
        val started = Backups.start(sard, agent.agentId, hostname, Chain.files(DATA, tree.excludePatterns(DATA)))
        val step = Backups.awaitFinished(sard, started.stepId)
        assertEquals("succeeded", step.status, step.message)
        return BackedUp(agent, tree, checkNotNull(step.snapshotId), started.sourceId)
    }

    companion object {
        @JvmField
        @RegisterExtension
        val sard = SardEnvironment()

        private const val DATA = Chain.DATA
        private const val RESTORE_DIR = "${AgentHost.STATE_DIR}/restore"
        private const val AGENT_UID = 65532
        private val LOCAL = Chain.repository("${AgentHost.STATE_DIR}/repo")
    }
}
