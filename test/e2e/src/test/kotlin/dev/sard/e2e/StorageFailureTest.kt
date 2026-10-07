// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.e2e

import org.junit.jupiter.api.extension.RegisterExtension
import java.time.Duration
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

/**
 * F2: what the storage refuses, the step reports. Each case breaks one thing of a working
 * repository, runs a backup through the console and expects the step `failed` within [BOUND],
 * with restic's cause in its message or its log; then the operator repairs that one thing and the
 * next run of the same source `succeeded`.
 */
class StorageFailureTest {
    @Test
    fun `S3, a wrong secret key`() {
        val key = garage.key("creds", garage.bucket("creds"), write = true)
        val agent = s3Agent("s3-creds", garage.url("creds", "repo"), key)
        Chain.ready(sard, agent)

        agent.host.put(ENV_FILE, TarFiles.ownedByAgent(GarageS3.Key(key.id, "0".repeat(64)).envFile()))
        failsThenSucceeds(agent, message = "Access Denied") { agent.host.put(ENV_FILE, TarFiles.ownedByAgent(key.envFile())) }
    }

    @Test
    fun `S3, a key without write access`() {
        val key = garage.key("readonly", garage.bucket("readonly"), write = true)
        val agent = s3Agent("s3-readonly", garage.url("readonly", "repo"), key)
        Chain.ready(sard, agent)

        garage.deny("readonly", "readonly", write = true)
        failsThenSucceeds(agent, log = "Operation is not allowed for this key") { garage.allow("readonly", "readonly", write = true) }
    }

    @Test
    fun `S3, the bucket does not exist`() {
        val key = garage.key("absent", garage.bucket("other"), write = true)
        val agent = s3Agent("s3-absent", garage.url("absent", "repo"), key)
        agent.host.put(Chain.PASSWORD_FILE, TarFiles.ownedByAgent("$PASSWORD\n"))
        connected(agent)

        failsThenSucceeds(agent, message = "repository does not exist") {
            garage.allow(garage.bucket("absent"), "absent", write = true)
            assertEquals(0, agent.host.repoInit(Chain.REPOSITORY).code)
        }
    }

    @Test
    fun `SFTP, the key is not authorized`() {
        val agent = sftpAgent("sftp-key", "${SftpServer.WRITABLE}/key")
        val publicKey = agent.host.sshClient(sftp.knownHostsLine())
        sftp.authorize(publicKey)
        Chain.ready(sard, agent)

        sftp.revoke(publicKey)
        failsThenSucceeds(agent, message = SFTP_SESSION) { sftp.authorize(publicKey) }
    }

    @Test
    fun `SFTP, the repository directory is read-only`() {
        val directory = "${SftpServer.WRITABLE}/readonly"
        val agent = sftpAgent("sftp-readonly", directory)
        sftp.authorize(agent.host.sshClient(sftp.knownHostsLine()))
        Chain.ready(sard, agent)

        sftp.exec("chmod", "-R", "a-w", directory)
        failsThenSucceeds(agent, log = "permission denied") { sftp.exec("chmod", "-R", "u+w", directory) }
    }

    @Test
    fun `SFTP, the repository directory does not exist`() {
        val agent = sftpAgent("sftp-absent", "${SftpServer.WRITABLE}/absent")
        sftp.authorize(agent.host.sshClient(sftp.knownHostsLine()))
        agent.host.put(Chain.PASSWORD_FILE, TarFiles.ownedByAgent("$PASSWORD\n"))
        connected(agent)

        failsThenSucceeds(agent, message = "repository does not exist") {
            assertEquals(0, agent.host.repoInit(Chain.REPOSITORY).code)
        }
    }

    @Test
    fun `SFTP, an unknown host key is not accepted`() {
        val agent = sftpAgent("sftp-hostkey", "${SftpServer.WRITABLE}/hostkey")
        val known = sftp.knownHostsLine()
        sftp.authorize(agent.host.sshClient(known))
        Chain.ready(sard, agent)

        agent.host.put(KNOWN_HOSTS, TarFiles(listOf(TarFiles.Entry("", ByteArray(0), TarFiles.READABLE))))
        failsThenSucceeds(agent, message = SFTP_SESSION) {
            agent.host.put(KNOWN_HOSTS, TarFiles(listOf(TarFiles.Entry("", known.toByteArray(), TarFiles.READABLE))))
        }
    }

    @Test
    fun `SFTP, the server is down`() {
        val agent = sftpAgent("sftp-down", "${SftpServer.WRITABLE}/down")
        sftp.authorize(agent.host.sshClient(sftp.knownHostsLine()))
        Chain.ready(sard, agent)

        Interruptions.stop(sftp.container)
        failsThenSucceeds(agent, message = SFTP_SESSION) { Interruptions.start(sftp.container) }
    }

    /**
     * A backup of a [SourceTree] fails within [BOUND] with [message] in the step's message and
     * [log] in its log (what the console shows); after [repair] the next run of the same source
     * succeeds.
     */
    private fun failsThenSucceeds(
        agent: EnrolledAgent,
        message: String? = null,
        log: String? = null,
        repair: () -> Unit,
    ) {
        agent.host.put(Chain.DATA, SourceTree.generate(Chain.SEED).files())
        val first = Backups.start(sard, agent.agentId, agent.host.hostname, Chain.files(Chain.DATA))
        val failed = Backups.awaitFinished(sard, first.stepId, BOUND)
        val logs = Backups.logs(sard, first.stepId)
        assertEquals("failed", failed.status, failed.message)
        message?.let { assertTrue(it in failed.message.orEmpty(), "no \"$it\" in the message: ${failed.message}") }
        log?.let { assertTrue(logs.any { line -> it in line }, "no \"$it\" in the log: $logs") }

        repair()
        val next = Backups.run(sard, first.sourceId)
        val succeeded = Backups.awaitFinished(sard, next.stepId, BOUND)
        assertEquals("succeeded", succeeded.status, succeeded.message)
    }

    private fun connected(agent: EnrolledAgent) {
        sard.track("agent-${agent.host.hostname}", AgentContainer.of(agent)).start()
        Await.until("Register of ${agent.host.hostname}") { Backups.connected(sard, agent.agentId) }
    }

    private fun s3Agent(
        hostname: String,
        url: String,
        key: GarageS3.Key,
    ): EnrolledAgent {
        val agent = AgentEnroller.enroll(sard, EnrollmentTokens.create(sard), hostname = hostname, local = Chain.repository(url, ENV_FILE))
        agent.host.put(ENV_FILE, TarFiles.ownedByAgent(key.envFile()))
        return agent
    }

    private fun sftpAgent(
        hostname: String,
        directory: String,
    ) = AgentEnroller.enroll(sard, EnrollmentTokens.create(sard), hostname = hostname, local = Chain.repository(sftp.url(directory)))

    companion object {
        @JvmField
        @RegisterExtension
        val sard = SardEnvironment()

        private val garage by lazy { GarageS3(sard).start() }
        private val sftp by lazy { SftpServer(sard).start() }

        /** How long a refused backup may take to fail: restic gives up on these at once. */
        private val BOUND = Duration.ofSeconds(60)
        private const val ENV_FILE = "${AgentHost.STATE_DIR}/s3.env"
        private const val KNOWN_HOSTS = "${AgentHost.SSH_DIR}/known_hosts"
        private const val PASSWORD = "f2-storage-failure"

        /**
         * restic's message for any failure of ssh: the cause (`subprocess ssh: …`) is a separate
         * stderr line that reaches the step's log, but not always (F2, docs/open-questions.md).
         */
        private const val SFTP_SESSION = "unable to start the sftp session"
    }
}
