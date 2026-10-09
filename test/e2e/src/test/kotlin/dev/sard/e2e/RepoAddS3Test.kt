// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.e2e

import org.junit.jupiter.api.extension.RegisterExtension
import org.testcontainers.images.builder.Transferable
import java.time.Duration
import kotlin.test.Test
import kotlin.test.assertContains
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

/**
 * The scenarios `@stand` of A8b-1 (`docs/specs/agent/host-setup.feature`, S3): the operator's
 * `sudo sard-agent repo add` on a host whose `/etc/sard` and state are volumes (Н24, ADR 0047),
 * against a bucket of [GarageS3]. The command runs as root in a container of the agent image
 * (the image has no sudo; root is what sudo gives), without systemd: the summary says to restart
 * the agent, and the test starts it.
 */
class RepoAddS3Test {
    @Test
    fun `an empty Garage bucket is connected by one command`() {
        val bucket = garage.bucket("add-empty")
        val key = garage.key("add-empty-rw", bucket, write = true)
        val host = AgentHost(sard, "add-empty", persistentEtc = true)

        val exit = host.repoAdd(NAME, garage.url(bucket, "main"), "--access-key-id", key.id, "--region", GarageS3.REGION, secretKey = key.secret)

        assertEquals(0, exit.code, exit.stderr)
        assertContains(exit.stdout, "created a new repository")
        assertTrue(Regex("\\b[0-9a-f]{64}\\b").containsMatchIn(exit.stdout), "no repository_id in ${exit.stdout}")
        assertEquals("65532:65532 600", host.stat("${AgentHost.SECRETS_DIR}/restic-$NAME.env"))
        assertEquals("65532:65532 600", host.stat("${AgentHost.SECRETS_DIR}/restic-$NAME.pass"))
        listOf(exit.stdout, exit.stderr).forEach { assertFalse(key.secret in it, "the secret key is in the output") }
    }

    @Test
    fun `a backup to S3 connected by the command restores byte for byte`() {
        val bucket = garage.bucket("add-chain")
        val key = garage.key("add-chain-rw", bucket, write = true)
        val url = garage.url(bucket, "main")
        val host = AgentHost(sard, "add-chain", persistentEtc = true)
        val agent = EnrolledAgent(AgentEnroller.enroll(host, EnrollmentTokens.create(sard)), host)
        val add = host.repoAdd(NAME, url, "--access-key-id", key.id, "--region", GarageS3.REGION, secretKey = key.secret)
        assertEquals(0, add.code, add.stderr)

        sard.track("agent-${host.hostname}", AgentContainer.of(agent)).start()
        Await.until("repository_id of $NAME in Register of ${host.hostname}") { Backups.repositoryId(sard, agent.agentId, NAME) != null }
        assertEquals("s3", Backups.repository(sard, agent.agentId, NAME)?.backend)
        Chain.backupRestores(sard, agent, url, key.env(), passwordFile = "${AgentHost.SECRETS_DIR}/restic-$NAME.pass")
    }

    @Test
    fun `a second host connects the existing repository with the password of the first`() {
        val bucket = garage.bucket("add-second")
        val key = garage.key("add-second-rw", bucket, write = true)
        val url = garage.url(bucket, "main")
        val first = AgentHost(sard, "add-first", persistentEtc = true)
        val created = first.repoAdd(NAME, url, "--access-key-id", key.id, "--region", GarageS3.REGION, secretKey = key.secret)
        assertEquals(0, created.code, created.stderr)
        val id = Regex("\\b[0-9a-f]{64}\\b").find(created.stdout)!!.value
        val password = first.revealPassword(NAME)

        val second = AgentHost(sard, "add-second", persistentEtc = true)
        val exit =
            second.repoAdd(
                NAME, url, "--access-key-id", key.id, "--region", GarageS3.REGION, "--password-from-file", PASSWORD_FILE,
                secretKey = key.secret,
                files = mapOf(PASSWORD_FILE to Transferable.of(password.toByteArray(), TarFiles.OWNER_ONLY)),
            )

        assertEquals(0, exit.code, exit.stderr)
        assertContains(exit.stdout, "attached an existing repository")
        assertContains(exit.stdout, id)
    }

    @Test
    fun `an unreachable Garage ends the command within the connect timeout`() {
        val bucket = garage.bucket("add-paused")
        val key = garage.key("add-paused-rw", bucket, write = true)
        val host = AgentHost(sard, "add-paused", persistentEtc = true)

        Interruptions.pause(garage.container)
        val started = System.nanoTime()
        val exit =
            try {
                host.repoAdd(NAME, garage.url(bucket, "main"), "--access-key-id", key.id, "--region", GarageS3.REGION, "--connect-timeout", "5s", secretKey = key.secret)
            } finally {
                Interruptions.unpause(garage.container)
            }

        assertTrue(Duration.ofNanos(System.nanoTime() - started) < Duration.ofSeconds(60), "the command took too long")
        assertEquals(6, exit.code, exit.stderr)
        assertContains(exit.stderr, "BACKEND_UNAVAILABLE")
        assertFalse(host.exists("${AgentHost.FRAGMENT_DIR}/repo-$NAME.yaml"), "a fragment was written")
    }

    @Test
    fun `a wrong secret key is refused with STORAGE_ACCESS_DENIED`() {
        val bucket = garage.bucket("add-wrong")
        val key = garage.key("add-wrong-rw", bucket, write = true)
        val host = AgentHost(sard, "add-wrong", persistentEtc = true)

        val exit = host.repoAdd(NAME, garage.url(bucket, "main"), "--access-key-id", key.id, "--region", GarageS3.REGION, secretKey = "0".repeat(64))

        refusedWith(host, exit, "STORAGE_ACCESS_DENIED")
    }

    @Test
    fun `a read-only key on a bucket that holds a repository is refused with STORAGE_ACCESS_DENIED`() {
        val bucket = garage.bucket("add-readonly")
        val writer = garage.key("add-readonly-rw", bucket, write = true)
        val first = AgentHost(sard, "add-readonly-1", persistentEtc = true)
        val created = first.repoAdd(NAME, garage.url(bucket, "main"), "--access-key-id", writer.id, "--region", GarageS3.REGION, secretKey = writer.secret)
        assertEquals(0, created.code, created.stderr)
        val password = first.revealPassword(NAME)
        val reader = garage.key("add-readonly-ro", bucket, write = false)
        val host = AgentHost(sard, "add-readonly-2", persistentEtc = true)

        // The password is given by flag: restic takes the lock only on an opened repository (П29),
        // so the refusal comes before any prompt and before any file is written.
        val exit =
            host.repoAdd(
                NAME, garage.url(bucket, "main"), "--access-key-id", reader.id, "--region", GarageS3.REGION, "--password-from-file", PASSWORD_FILE,
                secretKey = reader.secret,
                files = mapOf(PASSWORD_FILE to Transferable.of(password.toByteArray(), TarFiles.OWNER_ONLY)),
            )

        refusedWith(host, exit, "STORAGE_ACCESS_DENIED")
    }

    /**
     * The key may not create buckets and the bucket is not there. Measured on Garage (П30): restic
     * init prints `client.MakeBucket: Forbidden: Access key <id> is not allowed to create buckets`,
     * which is BUCKET_NOT_FOUND.
     */
    @Test
    fun `a missing bucket the key cannot create is refused with BUCKET_NOT_FOUND`() {
        val key = garage.key("add-absent-rw", garage.bucket("add-other"), write = true)
        val host = AgentHost(sard, "add-absent", persistentEtc = true)

        val exit = host.repoAdd(NAME, garage.url("add-absent", "main"), "--access-key-id", key.id, "--region", GarageS3.REGION, secretKey = key.secret)

        refusedWith(host, exit, "BUCKET_NOT_FOUND")
    }

    private fun refusedWith(
        host: AgentHost,
        exit: AgentHost.Exit,
        reason: String,
    ) {
        assertEquals(2, exit.code, exit.stderr)
        assertContains(exit.stderr, reason)
        assertFalse(host.exists("${AgentHost.FRAGMENT_DIR}/repo-$NAME.yaml"), "a fragment was written")
    }

    companion object {
        @JvmField
        @RegisterExtension
        val sard = SardEnvironment()

        private val garage by lazy { GarageS3(sard).start() }
        private const val NAME = "main"
        private const val PASSWORD_FILE = "/run/password"
    }
}
