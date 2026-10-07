// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.e2e

import org.junit.jupiter.api.extension.RegisterExtension
import kotlin.test.Test
import kotlin.test.assertEquals

/**
 * F2: the chain of [FullChainTest] — `repo init`, a files source, a backup through the console, the
 * snapshot, `restic restore`, the tree byte for byte — with the repository on remote storage
 * instead of the agent's disk (ADR 0047).
 *
 * - S3: a bucket of [GarageS3]; the key reaches restic only through the repository's env_file
 *   (owned by the agent, 0600), never through RESTIC_* or the agent's environment.
 * - SFTP: [SftpServer]; the service user's key and known_hosts in its passwd home, as an
 *   operator sets them up with ssh-keygen.
 */
class StorageChainTest {
    @Test
    fun `a backup to S3 through the console restores byte for byte`() {
        val bucket = garage.bucket("chain")
        val key = garage.key("chain-rw", bucket, write = true)
        val url = garage.url(bucket, "s3-chain")
        val agent = AgentEnroller.enroll(sard, EnrollmentTokens.create(sard), hostname = "s3-chain", local = Chain.repository(url, ENV_FILE))
        agent.host.put(ENV_FILE, TarFiles.ownedByAgent(key.envFile()))

        Chain.ready(sard, agent)
        assertEquals("s3", Backups.repository(sard, agent.agentId, Chain.REPOSITORY)?.backend)
        Chain.backupRestores(sard, agent, url, key.env())
    }

    @Test
    fun `a backup to SFTP through the console restores byte for byte`() {
        val url = sftp.url("${SftpServer.WRITABLE}/sftp-chain")
        val agent = AgentEnroller.enroll(sard, EnrollmentTokens.create(sard), hostname = "sftp-chain", local = Chain.repository(url))
        sftp.authorize(agent.host.sshClient(sftp.knownHostsLine()))

        Chain.ready(sard, agent)
        assertEquals("sftp", Backups.repository(sard, agent.agentId, Chain.REPOSITORY)?.backend)
        Chain.backupRestores(sard, agent, url)
    }

    companion object {
        @JvmField
        @RegisterExtension
        val sard = SardEnvironment()

        private val garage by lazy { GarageS3(sard).start() }
        private val sftp by lazy { SftpServer(sard).start() }

        private const val ENV_FILE = "${AgentHost.STATE_DIR}/s3.env"
    }
}
