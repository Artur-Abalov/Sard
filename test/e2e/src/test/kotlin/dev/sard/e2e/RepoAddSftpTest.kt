// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.e2e

import org.junit.jupiter.api.extension.RegisterExtension
import java.time.Duration
import kotlin.test.Test
import kotlin.test.assertContains
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNotNull
import kotlin.test.assertTrue

/**
 * The scenarios `@stand` of A8b-2 (`docs/specs/agent/host-setup.feature`, SFTP): the operator's
 * `sudo sard-agent repo add` on a host whose `/etc/sard` and state are volumes (Н24, ADR 0047),
 * against the stand's [SftpServer]. The command runs as root in a container of the agent image
 * (the image has no sudo; root is what sudo gives), without systemd. The host key is confirmed with
 * `--host-key-fingerprint` read from the server's own container: there is no terminal.
 */
class RepoAddSftpTest {
    @Test
    fun `a wrong fingerprint of the host key on the SFTP stand is a refusal of trust`() {
        val host = AgentHost(sard, "sftp-mismatch", persistentEtc = true)
        val other = "SHA256:" + "A".repeat(43)

        val exit = host.repoAdd(NAME, sftp.url("${SftpServer.WRITABLE}/mismatch"), "--host-key-fingerprint", other)

        assertEquals(5, exit.code, exit.stderr)
        assertContains(exit.stderr, "HOST_KEY_MISMATCH")
        assertContains(exit.stderr, sftp.fingerprint())
        assertFalse(host.exists("${AgentHost.SSH_DIR}/known_hosts"), "known_hosts was written")
    }

    @Test
    fun `the first run on the SFTP stand prints the public part of a new key and refuses`() {
        val host = AgentHost(sard, "sftp-first", persistentEtc = true)

        val exit = firstRun(host, "first")

        assertEquals(2, exit.code, exit.stderr)
        assertContains(exit.stderr, "SSH_KEY_NOT_AUTHORIZED")
        assertTrue(PUBLIC_KEY.containsMatchIn(exit.stdout), "no ssh-ed25519 key in ${exit.stdout}")
        assertFalse(host.exists("${AgentHost.FRAGMENT_DIR}/repo-$NAME.yaml"), "a fragment was written")
        assertTrue(host.exists("${AgentHost.SSH_DIR}/id_ed25519"), "the key was removed")
    }

    @Test
    fun `after the key is authorized the same command creates the repository on the SFTP stand`() {
        val host = AgentHost(sard, "sftp-repeat", persistentEtc = true)
        authorized(host, "repeat")

        val exit = connect(host, "repeat")

        assertEquals(0, exit.code, exit.stderr)
        assertContains(exit.stdout, "created a new repository")
        assertTrue(Regex("\\b[0-9a-f]{64}\\b").containsMatchIn(exit.stdout), "no repository_id in ${exit.stdout}")
        assertEquals("65532:65532 600", host.stat("${AgentHost.SSH_DIR}/known_hosts"))
    }

    @Test
    fun `a backup to SFTP connected by the command restores byte for byte`() {
        val host = AgentHost(sard, "sftp-add-chain", persistentEtc = true)
        val agent = EnrolledAgent(AgentEnroller.enroll(host, EnrollmentTokens.create(sard)), host)
        authorized(host, "chain")
        val add = connect(host, "chain")
        assertEquals(0, add.code, add.stderr)

        sard.track("agent-${host.hostname}", AgentContainer.of(agent)).start()
        Await.until("repository_id of $NAME in Register of ${host.hostname}") { Backups.repositoryId(sard, agent.agentId, NAME) != null }
        assertEquals("sftp", Backups.repository(sard, agent.agentId, NAME)?.backend)
        Chain.backupRestores(sard, agent, sftp.url("${SftpServer.WRITABLE}/chain"), passwordFile = "${AgentHost.SECRETS_DIR}/restic-$NAME.pass")
    }

    @Test
    fun `a read-only directory of the SFTP stand is refused with STORAGE_ACCESS_DENIED`() {
        val host = AgentHost(sard, "sftp-readonly", persistentEtc = true)
        authorized(host, "readonly")

        val exit = host.repoAdd(NAME, sftp.url("${SftpServer.READ_ONLY}/add"), "--host-key-fingerprint", sftp.fingerprint())

        assertEquals(2, exit.code, exit.stderr)
        assertContains(exit.stderr, "STORAGE_ACCESS_DENIED")
        assertContains(exit.stderr, "permission denied")
    }

    @Test
    fun `an SFTP server that fell off the network does not hold the command longer than the connect timeout`() {
        val host = AgentHost(sard, "sftp-silent", persistentEtc = true)
        authorized(host, "silent")
        assertEquals(0, connect(host, "silent").code)

        Interruptions.cut(sard, sftp.container)
        val started = System.nanoTime()
        val exit =
            try {
                host.repoAdd("again", sftp.url("${SftpServer.WRITABLE}/silent-again"), "--host-key-fingerprint", sftp.fingerprint(), "--connect-timeout", "5s")
            } finally {
                Interruptions.reconnect(sard, sftp.container, SftpServer.ALIAS)
            }

        assertTrue(Duration.ofNanos(System.nanoTime() - started) < Duration.ofSeconds(60), "the command took too long")
        assertEquals(6, exit.code, exit.stderr)
        assertContains(exit.stderr, "BACKEND_UNAVAILABLE")
    }

    /** `repo add` of [directory] on the stand with the fingerprint the server's admin gave. */
    private fun connect(
        host: AgentHost,
        directory: String,
    ) = host.repoAdd(NAME, sftp.url("${SftpServer.WRITABLE}/$directory"), "--host-key-fingerprint", sftp.fingerprint())

    /** The first run on a host with no key: it makes one, prints it, and is refused. */
    private fun firstRun(
        host: AgentHost,
        directory: String,
    ) = connect(host, directory)

    /** The first run, then the public key it printed let in on the server: the operator's two steps. */
    private fun authorized(
        host: AgentHost,
        directory: String,
    ) {
        val first = firstRun(host, directory)
        assertEquals(2, first.code, first.stderr)
        sftp.authorize(assertNotNull(PUBLIC_KEY.find(first.stdout), "no public key in ${first.stdout}").value)
    }

    companion object {
        @JvmField
        @RegisterExtension
        val sard = SardEnvironment()

        private val sftp by lazy { SftpServer(sard).start() }
        private const val NAME = "main"
        private val PUBLIC_KEY = Regex("ssh-ed25519 \\S+ \\S+")
    }
}
