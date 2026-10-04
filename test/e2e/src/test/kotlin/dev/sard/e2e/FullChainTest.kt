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
import kotlin.test.assertNull
import kotlin.test.assertTrue

/**
 * T2b, the backup chain with real containers and nothing staged by hand: the agent enrolls with
 * `sard-agent enroll`, creates its repository with `sard-agent repo init`, the console starts a
 * backup of a directory through the REST API, the files plugin runs it with restic, the server
 * records the result and the snapshot, and `restic restore` of that snapshot gives back the tree
 * byte for byte.
 *
 * The agent's host is [AgentHost]: a local restic repository on its state volume, the password
 * file `repo init --generate-password` writes and the test never reads. The data is a
 * [SourceTree] whose reference is taken when it is generated, before the backup.
 */
class FullChainTest {
    /**
     * Also the `@e2e` scenario of docs/specs/agent/repo-init.feature, "После repo init и
     * перезапуска агента сервер знает repository_id": the agent runs before the repository
     * exists, the operator runs `repo init` on the host, the agent service restarts.
     */
    @Test
    fun `a backup through the console restores byte for byte, after repo init and a restart of the agent`() {
        val agent = AgentEnroller.enroll(sard, EnrollmentTokens.create(sard), hostname = "chain", local = LOCAL)
        val tree = SourceTree.generate(SEED)
        agent.host.put(DATA, tree.files())
        val container = sard.track("agent-chain", AgentContainer.of(agent)).apply { start() }
        Await.until("Register of the agent") { Backups.connected(sard, agent.agentId) }
        assertNull(assertNotNull(Backups.repository(sard, agent.agentId, REPOSITORY)).id, "a repository_id before repo init")

        val init = agent.host.repoInit(REPOSITORY, "--generate-password")
        assertEquals(0, init.code, init.stderr)
        container.dockerClient.restartContainerCmd(container.containerId).exec()
        val repositoryId = INIT_ID.find(init.stdout)?.groupValues?.get(1)
        val backend = INIT_BACKEND.find(init.stdout)?.groupValues?.get(1)
        val known = Await.value("repository_id of $REPOSITORY after the restart") { Backups.repository(sard, agent.agentId, REPOSITORY)?.takeIf { it.id != null } }
        assertEquals(repositoryId to backend, known.id to known.backend)

        val started = Backups.start(sard, agent.agentId, "chain", config(DATA, tree.excludePatterns(DATA)))
        val step = Backups.awaitFinished(sard, started.stepId)
        assertEquals("succeeded", step.status, step.message)
        assertEquals("succeeded", Backups.runStatus(sard, started.runId))
        val snapshot = assertNotNull(Backups.snapshot(sard, started.stepId))
        assertEquals(listOf(repositoryId, step.snapshotId, "false"), listOf(snapshot.repositoryId, snapshot.snapshotId, snapshot.partial.toString()))

        val restored = agent.host.restore(REPOSITORY_URL, PASSWORD_FILE, snapshot.snapshotId, DATA)
        assertEquals(emptyList(), TreeDiff.of(tree.expected, restored))
    }

    @Test
    fun `a files step reaches the agent, runs, and its result is stored and acknowledged`() {
        val (agent, container) = readyAgent("transport")
        agent.host.put(DATA, SourceTree.generate(SEED).files())

        val started = Backups.start(sard, agent.agentId, "transport", config(DATA))
        val step = Backups.awaitFinished(sard, started.stepId)

        assertEquals("succeeded", step.status, step.message)
        assertTrue(step.started, "the step never went running (no progress reached the server)")
        assertEquals(Backups.repositoryId(sard, agent.agentId, REPOSITORY), step.repositoryId)
        Await.until("the agent's tombstone of ${started.stepId}") { container.holds("acked", started.stepId) }
        assertFalse(container.holds("results", started.stepId), "the agent still holds the result: no ResultAck reached it")
    }

    @Test
    fun `a path that does not exist fails the step and names the path`() {
        val (agent, _) = readyAgent("missing")
        val missing = "$DATA/missing"

        val started = Backups.start(sard, agent.agentId, "missing", config(missing))
        val step = Backups.awaitFinished(sard, started.stepId)

        assertEquals("failed", step.status)
        val message = step.message.orEmpty()
        assertTrue("\"$missing\"" in message && "no such file or directory" in message, "the message does not name the path: $message")
        assertEquals("failed", Backups.runStatus(sard, started.runId))
        assertNull(Backups.snapshot(sard, started.stepId), "a snapshot of a backup that never ran")
    }

    @Test
    fun `an unreadable file fails the step, and its snapshot restores the readable files`() {
        val (agent, _) = readyAgent("unreadable")
        val tree = SourceTree.generate(SEED, unreadable = true)
        agent.host.put(DATA, tree.files())

        val started = Backups.start(sard, agent.agentId, "unreadable", config(DATA, tree.excludePatterns(DATA)))
        val step = Backups.awaitFinished(sard, started.stepId)

        assertEquals("failed", step.status)
        val unreadable = assertNotNull(tree.unreadablePath(DATA))
        assertTrue("\"$unreadable\"" in step.message.orEmpty(), "the message does not name the unreadable file: ${step.message}")
        assertEquals(true, step.partial)
        val snapshot = assertNotNull(Backups.snapshot(sard, started.stepId), "no snapshot of the partial backup")
        assertEquals(listOf(step.repositoryId, step.snapshotId, "true"), listOf(snapshot.repositoryId, snapshot.snapshotId, snapshot.partial.toString()))

        val restored = agent.host.restore(REPOSITORY_URL, PASSWORD_FILE, snapshot.snapshotId, DATA)
        assertEquals(emptyList(), TreeDiff.of(tree.expected, restored))
    }

    /** An enrolled host with its repository initialised and the agent connected, the repository_id known to the server. */
    private fun readyAgent(hostname: String): Pair<EnrolledAgent, GenericContainer<*>> {
        val agent = AgentEnroller.enroll(sard, EnrollmentTokens.create(sard), hostname = hostname, local = LOCAL)
        val init = agent.host.repoInit(REPOSITORY, "--generate-password")
        assertEquals(0, init.code, init.stderr)
        val container = sard.track("agent-$hostname", AgentContainer.of(agent)).apply { start() }
        Await.until("repository_id of $REPOSITORY in Register of $hostname") { Backups.repositoryId(sard, agent.agentId, REPOSITORY) != null }
        return agent to container
    }

    /** files plugin config (agent/plugins/files/config.go). */
    private fun config(
        path: String,
        exclude: List<String> = emptyList(),
    ) = """{"paths":["$path"],"exclude":[${exclude.joinToString(",") { "\"$it\"" }}]}"""

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

    companion object {
        @JvmField
        @RegisterExtension
        val sard = SardEnvironment()

        private const val SEED = 20261004
        private const val REPOSITORY = "main"
        private const val DATA = "${AgentHost.STATE_DIR}/data"
        private const val REPOSITORY_URL = "${AgentHost.STATE_DIR}/repo"
        private const val PASSWORD_FILE = "${AgentHost.STATE_DIR}/$REPOSITORY.pass"

        /** The summary of `sard-agent repo init` (agent/cmd/sard-agent/repo_init_run.go). */
        private val INIT_ID = Regex("repository_id: ([0-9a-f]{64})")
        private val INIT_BACKEND = Regex("backend: +([a-z0-9]+)")

        private val LOCAL =
            """
            repositories:
              - name: $REPOSITORY
                url: $REPOSITORY_URL
                password_file: $PASSWORD_FILE
            """.trimIndent() + "\n"
    }
}
