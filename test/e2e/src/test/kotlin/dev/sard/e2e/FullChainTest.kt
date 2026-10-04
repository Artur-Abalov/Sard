// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.e2e

import org.junit.jupiter.api.extension.RegisterExtension
import org.testcontainers.containers.GenericContainer
import java.security.MessageDigest
import java.util.HexFormat
import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNotNull
import kotlin.test.assertTrue

/**
 * T2b, the backup chain with real containers and nothing staged by hand: the agent enrolls with
 * `sard-agent enroll`, creates its repository with `sard-agent repo init`, the console starts a
 * backup of a directory through the REST API, the files plugin runs it with restic, and the
 * server records the result and the snapshot.
 *
 * The agent's host is [AgentHost]: a local restic repository on its state volume, the password
 * file `repo init --generate-password` writes and the test never reads.
 */
class FullChainTest {
    @Test
    fun `a files step reaches the agent, runs, and its result is stored and acknowledged`() {
        val agent = AgentEnroller.enroll(sard, EnrollmentTokens.create(sard), hostname = "transport", local = LOCAL)
        agent.host.put(DATA, smallTree())
        assertEquals(0, agent.host.repoInit(REPOSITORY, "--generate-password").code)
        val container = sard.track("agent-transport", AgentContainer.of(agent)).apply { start() }
        val repositoryId = Await.value("repository_id of $REPOSITORY in Register") { Backups.repositoryId(sard, agent.agentId, REPOSITORY) }

        val started = Backups.start(sard, agent.agentId, "transport", """{"paths":["$DATA"]}""")
        val step = Backups.awaitFinished(sard, started.stepId)

        assertEquals("succeeded", step.status, step.message)
        assertTrue(step.started, "the step never went running (no progress reached the server)")
        assertEquals(repositoryId, step.repositoryId)
        assertEquals("succeeded", Backups.runStatus(sard, started.runId))
        val snapshot = assertNotNull(Backups.snapshot(sard, started.stepId))
        assertEquals(repositoryId to step.snapshotId, snapshot.repositoryId to snapshot.snapshotId)
        Await.until("the agent's tombstone of ${started.stepId}") { container.holds("acked", started.stepId) }
        assertFalse(container.holds("results", started.stepId), "the agent still holds the result: no ResultAck reached it")
    }

    /**
     * Whether the agent's executor holds [stepId]'s file in [dir] (agent/internal/executor/store.go):
     * a result waiting for its ack is `results/<sha256(command_id)>.json`, an acknowledged one
     * moves to the tombstone `acked/<same>.json` and is never sent again.
     */
    private fun GenericContainer<*>.holds(
        dir: String,
        stepId: UUID,
    ): Boolean {
        val digest = MessageDigest.getInstance("SHA-256").digest(stepId.toString().toByteArray())
        val path = "${AgentHost.EXECUTOR_DIR}/$dir/${HexFormat.of().formatHex(digest)}.json"
        return runCatching { copyFileFromContainer(path) { it.readAllBytes() } }.isSuccess
    }

    private fun smallTree() =
        TarFiles(
            listOf(
                TarFiles.Entry("", null, TarFiles.OWNER_DIR),
                TarFiles.Entry("hello.txt", "hello, sard\n".toByteArray(), TarFiles.OWNER_ONLY),
            ),
        )

    companion object {
        @JvmField
        @RegisterExtension
        val sard = SardEnvironment()

        private const val REPOSITORY = "main"
        private const val DATA = "${AgentHost.STATE_DIR}/data"

        private val LOCAL =
            """
            repositories:
              - name: $REPOSITORY
                url: ${AgentHost.STATE_DIR}/repo
                password_file: ${AgentHost.STATE_DIR}/$REPOSITORY.pass
            """.trimIndent() + "\n"
    }
}
