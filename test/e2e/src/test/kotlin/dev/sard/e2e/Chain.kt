// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.e2e

import org.testcontainers.containers.GenericContainer
import kotlin.test.assertEquals
import kotlin.test.assertNotNull

/**
 * The backup chain of [FullChainTest] for any restic backend: the repository `main` of an enrolled
 * host, its password file written by `repo init --generate-password` and never read by a test.
 */
internal object Chain {
    const val REPOSITORY = "main"
    const val PASSWORD_FILE = "${AgentHost.STATE_DIR}/$REPOSITORY.pass"
    const val DATA = "${AgentHost.STATE_DIR}/data"
    const val SEED = 20261004

    /** The `repositories` section of the host's config: `main` at [url], with [envFile] if any. */
    fun repository(
        url: String,
        envFile: String? = null,
    ): String =
        """
        repositories:
          - name: $REPOSITORY
            url: $url
            password_file: $PASSWORD_FILE
        """.trimIndent() + "\n" + (envFile?.let { "    env_file: $it\n" } ?: "")

    /** `repo init` on [agent]'s host, then the agent service, until the server knows the repository_id. */
    fun ready(
        sard: SardEnvironment,
        agent: EnrolledAgent,
    ): GenericContainer<*> {
        val init = agent.host.repoInit(REPOSITORY, "--generate-password")
        assertEquals(0, init.code, init.stderr)
        val container = sard.track("agent-${agent.host.hostname}", AgentContainer.of(agent)).apply { start() }
        Await.until("repository_id of $REPOSITORY in Register of ${agent.host.hostname}") { Backups.repositoryId(sard, agent.agentId, REPOSITORY) != null }
        return container
    }

    /**
     * A [SourceTree] on [agent]'s host backed up through the console into [url], and its snapshot
     * restored with `restic restore` ([env]: the backend's credentials, [passwordFile]: where the
     * repository's password is): the tree comes back byte for byte.
     */
    fun backupRestores(
        sard: SardEnvironment,
        agent: EnrolledAgent,
        url: String,
        env: Map<String, String> = emptyMap(),
        passwordFile: String = PASSWORD_FILE,
    ) {
        val tree = SourceTree.generate(SEED)
        agent.host.put(DATA, tree.files())

        val started = Backups.start(sard, agent.agentId, agent.host.hostname, files(DATA, tree.excludePatterns(DATA)))
        val step = Backups.awaitFinished(sard, started.stepId)
        assertEquals("succeeded", step.status, step.message)
        val snapshot = assertNotNull(Backups.snapshot(sard, started.stepId))
        assertEquals(listOf(step.repositoryId, step.snapshotId, "false"), listOf(snapshot.repositoryId, snapshot.snapshotId, snapshot.partial.toString()))

        val restored = agent.host.restore(url, passwordFile, snapshot.snapshotId, DATA, env)
        assertEquals(emptyList(), TreeDiff.of(tree.expected, restored))
    }

    /** files plugin config (agent/plugins/files/config.go). */
    fun files(
        path: String,
        exclude: List<String> = emptyList(),
    ) = """{"paths":["$path"],"exclude":[${exclude.joinToString(",") { "\"$it\"" }}]}"""
}
