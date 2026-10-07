// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.e2e

import org.junit.jupiter.api.extension.RegisterExtension
import java.time.Duration
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

/**
 * F2: a restic lock that an interrupted process left in a remote repository (A6b: every backup
 * runs with `--retry-lock 5m`, ADR 0029).
 *
 * - A backup interrupted by a crash of the agent (`docker kill`, restic gets SIGKILL) leaves its
 *   lock: a shared one, so the next backup goes ahead.
 * - An operator's `restic check` killed on the host leaves an exclusive lock: a backup is refused
 *   at once, by the `restic cat` before it, and the message says the repository is locked; after
 *   `restic unlock --remove-all` it runs.
 */
class StorageLockTest {
    @Test
    fun `the lock of a backup killed with the agent does not stop the next backup`() {
        val agent = sftpAgent("lock-crash", "${SftpServer.WRITABLE}/crash")
        val container = Chain.ready(sard, agent)

        val started = Backups.start(sard, agent.agentId, agent.host.hostname, SLOW, plugin = "e2e-slow")
        Await.until("restic backup streaming step ${started.stepId}") { Backups.bytesProcessed(sard, started.stepId) > STREAMING }
        Interruptions.kill(container)
        Await.until("the lock of the killed backup") { locks(agent).isNotEmpty() }
        Interruptions.start(container)
        assertEquals("failed", Backups.awaitFinished(sard, started.stepId).status)

        val next = Backups.awaitFinished(sard, Backups.run(sard, started.sourceId).stepId, BOUND)
        assertEquals("succeeded", next.status, next.message)
    }

    @Test
    fun `an exclusive lock of a killed restic check refuses the backup until restic unlock`() {
        val agent = sftpAgent("lock-check", "${SftpServer.WRITABLE}/check")
        Chain.ready(sard, agent)
        agent.host.put(Chain.DATA, SourceTree.generate(Chain.SEED).files())
        val first = Backups.start(sard, agent.agentId, agent.host.hostname, Chain.files(Chain.DATA))
        assertEquals("succeeded", Backups.awaitFinished(sard, first.stepId, BOUND).status)

        // The operator checks the repository on the host, slowly, and the session dies with it.
        val check = agent.host.launch(*restic(agent, "check", "--read-data", "--limit-download", "16"))
        Await.until("the exclusive lock of restic check") { locks(agent).isNotEmpty() }
        Interruptions.kill(check)

        // Today the refusal is at once: `restic cat` before the backup has no --retry-lock, so the
        // backup's 5 minutes never start (F2, docs/open-questions.md). A fix makes this wait.
        val refused = Backups.awaitFinished(sard, Backups.run(sard, first.sourceId).stepId, Await.TIMEOUT)
        assertEquals("failed", refused.status, refused.message)
        assertTrue("restic cat: $LOCKED" in refused.message.orEmpty(), "no \"restic cat: $LOCKED\" in the message: ${refused.message}")

        assertEquals(0, agent.host.run(*restic(agent, "unlock", "--remove-all")).code)
        val next = Backups.awaitFinished(sard, Backups.run(sard, first.sourceId).stepId, BOUND)
        assertEquals("succeeded", next.status, next.message)
    }

    /** The ids of the locks in [agent]'s repository (`restic list locks --no-lock`, on its host). */
    private fun locks(agent: EnrolledAgent): List<String> {
        val exit = agent.host.run(*restic(agent, "list", "locks", "--no-lock"))
        check(exit.code == 0) { "restic list locks exited ${exit.code}: ${exit.stderr}" }
        return exit.stdout.lines().filter { it.isNotBlank() }
    }

    /** restic [args] on [agent]'s repository, as the operator runs it on the host. */
    private fun restic(
        agent: EnrolledAgent,
        vararg args: String,
    ) = arrayOf(AgentImage.RESTIC_BINARY, "--repo", urls.getValue(agent.host.hostname), "--password-file", Chain.PASSWORD_FILE, "--no-cache", *args)

    private val urls = mutableMapOf<String, String>()

    private fun sftpAgent(
        hostname: String,
        directory: String,
    ): EnrolledAgent {
        val url = sftp.url(directory).also { urls[hostname] = it }
        val agent = AgentEnroller.enroll(sard, EnrollmentTokens.create(sard), hostname = hostname, local = Chain.repository(url), image = E2e.standAgentImage)
        sftp.authorize(agent.host.sshClient(sftp.knownHostsLine()))
        return agent
    }

    companion object {
        @JvmField
        @RegisterExtension
        val sard = SardEnvironment()

        private val sftp by lazy { SftpServer(sard).start() }

        /** `e2e-slow` config (ADR 0036): 60 s of data, long enough to be killed mid-way. */
        private const val SLOW = """{"size":${60 shl 20},"rate":${1 shl 20},"chunk":${64 shl 10},"seed":20261007}"""

        /** Progress that proves restic backup reads the data, and so holds its lock. */
        private const val STREAMING = 2L shl 20

        private val BOUND = Duration.ofMinutes(7)

        /** The agent's message for restic's exit code 11 (agent/internal/restic/restic.go). */
        private const val LOCKED = "repository is locked by another process"
    }
}
