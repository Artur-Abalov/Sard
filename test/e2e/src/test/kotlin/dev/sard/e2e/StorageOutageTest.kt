// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.e2e

import org.junit.jupiter.api.extension.RegisterExtension
import java.time.Duration
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

/**
 * F2: the storage goes away while a backup streams to it. The source is `e2e-slow` (ADR 0036) on
 * the stand's agent: [SECONDS] seconds of data at 1 MiB/s, so the outage lands mid-backup.
 *
 * What restic does decides the outcome; the cases pin it down (ADR 0047, docs/open-questions.md):
 * - S3 stopped and back within restic's retries: the backup waits and succeeds.
 * - SFTP stopped (sshd gone, the connection closed): the step fails when the data ends, with
 *   ssh's exit in the message; the next run after the server is back succeeds.
 * - SFTP off the network without a word (no FIN, no RST): with `ServerAliveInterval` in the
 *   service user's `~/.ssh/config` ssh gives up and the step fails; without it the step waits for
 *   the network, as long as it takes — still `running` well after its data ended — and succeeds
 *   once the network is back.
 */
class StorageOutageTest {
    @Test
    fun `S3 stopped mid-backup and back within restic's retries, the backup succeeds`() {
        val key = garage.key("outage", garage.bucket("outage"), write = true)
        val agent = slowAgent("s3-outage", Chain.repository(garage.url("outage", "repo"), ENV_FILE))
        agent.host.put(ENV_FILE, TarFiles.ownedByAgent(key.envFile()))
        Chain.ready(sard, agent)

        val started = runningBackup(agent)
        Interruptions.stop(garage.container)
        Await.during("step ${started.stepId} running while S3 is down", OUTAGE) { Backups.step(sard, started.stepId)?.status == "running" }
        Interruptions.start(garage.container)

        val step = Backups.awaitFinished(sard, started.stepId, Duration.ofMinutes(3))
        assertEquals("succeeded", step.status, step.message)
    }

    @Test
    fun `SFTP stopped mid-backup, the step fails naming ssh, and the next run succeeds`() {
        val agent = sftpAgent("sftp-outage", "${SftpServer.WRITABLE}/outage")
        Chain.ready(sard, agent)

        val started = runningBackup(agent)
        Interruptions.stop(sftp.container)
        try {
            val step = Backups.awaitFinished(sard, started.stepId, BOUND)
            assertEquals("failed", step.status, step.message)
            assertTrue(SSH_EXITED in step.message.orEmpty(), "no \"$SSH_EXITED\" in the message: ${step.message}")
        } finally {
            Interruptions.start(sftp.container)
        }
        assertEquals("succeeded", Backups.awaitFinished(sard, Backups.run(sard, started.sourceId).stepId, BOUND).status)
    }

    @Test
    fun `SFTP off the network with ServerAliveInterval, the step fails, and the next run succeeds`() {
        val agent = sftpAgent("sftp-alive", "${SftpServer.WRITABLE}/alive")
        agent.host.put(SSH_CONFIG, TarFiles(listOf(TarFiles.Entry("", KEEPALIVE.toByteArray(), TarFiles.READABLE))))
        Chain.ready(sard, agent)

        val started = runningBackup(agent)
        Interruptions.cut(sard, sftp.container)
        try {
            val step = Backups.awaitFinished(sard, started.stepId, BOUND)
            assertEquals("failed", step.status, step.message)
            assertTrue(SSH_EXITED in step.message.orEmpty(), "no \"$SSH_EXITED\" in the message: ${step.message}")
        } finally {
            Interruptions.reconnect(sard, sftp.container, SftpServer.ALIAS)
        }
        assertEquals("succeeded", Backups.awaitFinished(sard, Backups.run(sard, started.sourceId).stepId, BOUND).status)
    }

    @Test
    fun `SFTP off the network without keepalive, the step waits for the network and then succeeds`() {
        val agent = sftpAgent("sftp-silent", "${SftpServer.WRITABLE}/silent")
        Chain.ready(sard, agent)

        val started = runningBackup(agent)
        Interruptions.cut(sard, sftp.container)
        try {
            // Well past the end of the data and past the bound the other cases fail within.
            Await.during("step ${started.stepId} running while the server is off the network", BOUND) { Backups.step(sard, started.stepId)?.status == "running" }
        } finally {
            Interruptions.reconnect(sard, sftp.container, SftpServer.ALIAS)
        }
        val step = Backups.awaitFinished(sard, started.stepId, BOUND)
        assertEquals("succeeded", step.status, step.message)
    }

    /**
     * Starts a backup of [SECONDS] seconds of `e2e-slow` data and waits until restic streams it to
     * the storage: progress past [STREAMING] (`running` alone comes before `restic cat`).
     */
    private fun runningBackup(agent: EnrolledAgent): Backups.Started {
        val started = Backups.start(sard, agent.agentId, agent.host.hostname, SLOW, plugin = "e2e-slow")
        Await.until("step ${started.stepId} streaming") { Backups.bytesProcessed(sard, started.stepId) > STREAMING }
        return started
    }

    private fun slowAgent(
        hostname: String,
        local: String,
    ) = AgentEnroller.enroll(sard, EnrollmentTokens.create(sard), hostname = hostname, local = local, image = E2e.standAgentImage)

    private fun sftpAgent(
        hostname: String,
        directory: String,
    ): EnrolledAgent {
        val agent = slowAgent(hostname, Chain.repository(sftp.url(directory)))
        sftp.authorize(agent.host.sshClient(sftp.knownHostsLine()))
        return agent
    }

    companion object {
        @JvmField
        @RegisterExtension
        val sard = SardEnvironment()

        private val garage by lazy { GarageS3(sard).start() }
        private val sftp by lazy { SftpServer(sard).start() }

        private const val SECONDS = 20
        private const val RATE = 1 shl 20

        /** Progress that proves restic backup reads the data: two of its [SECONDS] seconds. */
        private const val STREAMING = 2L * RATE

        /** `e2e-slow` config (ADR 0036): [SECONDS] s of data at [RATE]. */
        private const val SLOW = """{"size":${SECONDS * RATE},"rate":$RATE,"chunk":${64 shl 10},"seed":20261007}"""

        /** Shorter than restic's retries of one request (backoff up to minutes): it waits it out. */
        private val OUTAGE = Duration.ofSeconds(30)

        /** The data's end plus a margin: a step that fails, fails by then. */
        private val BOUND = Duration.ofSeconds(SECONDS + 60L)

        /** restic's message when ssh died under it (the sftp backend). */
        private const val SSH_EXITED = "ssh command exited"

        private const val ENV_FILE = "${AgentHost.STATE_DIR}/s3.env"
        private const val SSH_CONFIG = "${AgentHost.SSH_DIR}/config"

        /** ssh drops a peer silent for 3 × 5 s (what docs/operator recommends for SFTP). */
        private const val KEEPALIVE = "ServerAliveInterval 5\nServerAliveCountMax 3\n"
    }
}
