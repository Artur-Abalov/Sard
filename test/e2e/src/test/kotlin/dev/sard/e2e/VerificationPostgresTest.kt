// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.e2e

import com.github.dockerjava.api.model.Bind
import com.github.dockerjava.api.model.Volume
import org.junit.jupiter.api.extension.RegisterExtension
import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertContains
import kotlin.test.assertEquals
import kotlin.test.assertTrue

/**
 * V5, phase 3: a verifier restores snapshots of the postgresql plugin into a sandbox, the official image of the
 * source's major version, and measures the copy there (strategy of the task, checks 1, 4, 5, 7). A PostgreSQL 18
 * source host S backs up into a local repository on a volume it shares with the verifier V, which mounts it read
 * only ([VerifierStand]). Every verification is started with «verify now» and read from `restore_verifications`.
 */
class VerificationPostgresTest {
    // Check 1 for postgresql, check 7: every stage passes in the sandbox, level 5, and nothing of it is left.
    @Test
    fun `a snapshot of postgresql passes every stage in its sandbox to level 5 and leaves nothing`() {
        stand()
        val volumes = verifications.volumes()
        val snapshot = backup("pg-passing")

        val record = verifications.verify(snapshot)

        assertEquals("passed", record.status, record.reason)
        assertEquals(5, record.level)
        assertEquals(PG_STAGES, record.stageNames)
        assertTrue(record.restoreMillis!! > 0, "no restore time")
        assertContains(record.outputs[PG_STAGES.indexOf("sandbox")], "postgres 18.")
        assertLeftNothing(volumes)
    }

    // Check 4: the source's fingerprint, rewritten with its tag, says one table more than the copy has.
    @Test
    fun `a copy that differs from the fingerprint fails at level 4 naming the difference`() {
        stand()
        val volumes = verifications.volumes()
        val snapshot = backup("pg-differs")
        forgeTables(snapshot)

        val record = verifications.verify(snapshot)

        assertEquals("failed", record.status, record.reason)
        assertEquals(3, record.level)
        assertEquals(PG_STAGES, record.stageNames)
        assertContains(record.reason.orEmpty(), "tables")
        assertLeftNothing(volumes)
    }

    // Check 5: the source has an extension the image of its version lacks; the backup is not judged.
    @Test
    fun `an extension the image lacks cannot be verified and says which`() {
        stand()
        val snapshot = backup("pg-extension", database = EXTENDED)

        val record = verifications.verify(snapshot)

        assertEquals("cannot_verify", record.status, record.reason)
        assertContains(record.reason.orEmpty(), "no extension sard_e2e")
        assertEquals(emptyList(), verifications.sandboxes())
        assertEquals(emptyList(), verifier.workVolume())
    }

    // Check 5: the room a copy needs (verify.space_headroom on the verifier) is more than the work volume has.
    @Test
    fun `a work volume without room for the copy cannot be verified`() {
        stand()
        val snapshot = backup("pg-room")
        verifier.start("  space_headroom: ${8L shl 30}\n")
        try {
            val record = verifications.verify(snapshot)

            assertEquals("cannot_verify", record.status, record.reason)
            assertEquals(listOf("snapshot", "fingerprint_tag", "work_volume", "cleanup"), record.stageNames)
            assertContains(record.reason.orEmpty(), "bytes free")
        } finally {
            verifier.start()
        }
    }

    // Check 5: a stage that runs out of its time is a failure of the infrastructure, tried again (V4), never the backup's.
    @Test
    fun `a stage that runs out of time is an infrastructure failure`() {
        stand()
        val snapshot = backup("pg-slow")
        verifier.start("  stage_limits: {restore: 1s}\n")
        try {
            val record = verifications.verify(snapshot)

            assertEquals("infrastructure_failure", record.status, record.reason)
            assertContains(record.reason.orEmpty(), "stage sandbox ran out of time")
            assertEquals(emptyList(), verifications.sandboxes())
        } finally {
            verifier.start()
        }
    }

    /** No sandbox container, no volume it made, no copy on the work volume. */
    private fun assertLeftNothing(volumesBefore: Set<String>) {
        assertEquals(emptyList(), verifications.sandboxes(), "a sandbox outlived its verification")
        assertEquals(emptySet(), verifications.volumes() - volumesBefore, "a sandbox left a volume")
        assertEquals(emptyList(), verifier.workVolume(), "the verification left a copy on the verifier")
    }

    /** Backs up [database] as a new source [name] and returns its snapshot's id (snapshots.id). */
    private fun backup(
        name: String,
        database: String = "app",
    ): UUID {
        val started = Backups.start(sard, source.agentId, name, PgAgent.configK(database = database), plugin = "postgresql", repository = REPOSITORY)
        // Verification is on by default (V4); «verify now» does not need it, the cycle would race it.
        verifications.enable(started.sourceId, enabled = false)
        val step = Backups.awaitFinished(sard, started.stepId)
        assertEquals("succeeded", step.status, step.message)
        return verifications.snapshotOfStep(started.stepId)
    }

    /**
     * Rewrites the fingerprint of [snapshot] with one table more, tags the snapshot in the repository with the hash
     * of the new one (as a source that measured wrongly would have), and gives the server both.
     */
    private fun forgeTables(snapshot: UUID) {
        val (restic, fingerprint) =
            verifications.query("SELECT snapshot_id, fingerprint FROM snapshots WHERE id = ?", snapshot) { it.getString(1) to it.getBytes(2) }!!
        val text = String(fingerprint)
        val tables = TABLES.find(text) ?: error("no tables in $text")
        val forged = text.replace(tables.value, tables.value.replace("\"${tables.groupValues[1]}\"", "\"${tables.groupValues[1].toInt() + 1}\"")).toByteArray()
        val (old, new) = verifications.sha256(fingerprint) to verifications.sha256(forged)
        val tag = sourceRestic("tag", "--remove", "sard.fp=$old", "--add", "sard.fp=$new", restic)
        assertEquals(0, tag.code, tag.stderr)
        val listed = sourceRestic("snapshots", "--json", "--tag", "sard.fp=$new")
        val id = checkNotNull(SNAPSHOT_ID.find(listed.stdout)) { listed.stdout }.groupValues[1]
        verifications.update("UPDATE snapshots SET snapshot_id = ?, fingerprint = ?, fingerprint_sha256 = ? WHERE id = ?", id, forged, new, snapshot)
    }

    private fun sourceRestic(vararg args: String): AgentHost.Exit =
        source.host.run(AgentImage.RESTIC_BINARY, *args, "--repo", "/srv/$REPOSITORY", "--password-file", Chain.PASSWORD_FILE, "--no-cache")

    companion object {
        @JvmField
        @RegisterExtension
        val sard = SardEnvironment()

        private val verifications = Verifications(sard)
        private const val REPOSITORY = Chain.REPOSITORY
        private const val EXTENDED = "extended"
        private val TABLES = Regex("\\{\"name\":\"tables\",\"rule\":\"exact\",\"value\":\"(\\d+)\"}")
        private val SNAPSHOT_ID = Regex("\"id\":\"([0-9a-f]{64})\"")
        private val PG_STAGES =
            listOf("snapshot", "fingerprint_tag", "work_volume", "restic_check", "sandbox", "restore", "start", "fingerprint", "cleanup")

        private val repository by lazy { verifications.agentsVolume() }

        /**
         * The server S backs up: the seeded `app`, and `extended` with an extension of its own, installed into the
         * server's share directory by hand as a third-party extension would be, which the official image lacks.
         */
        private val db by lazy {
            PgServer(sard, 18).start().apply {
                seed()
                put("/usr/share/postgresql/18/extension/sard_e2e.control", "default_version = '1.0'\nrelocatable = true\n".toByteArray())
                put("/usr/share/postgresql/18/extension/sard_e2e--1.0.sql", "CREATE FUNCTION sard_e2e() RETURNS int LANGUAGE sql AS 'SELECT 1';\n".toByteArray())
                sql("create database $EXTENDED")
                sql("create extension sard_e2e", "create table x(id int)", "grant select on x to backup", database = EXTENDED)
            }
        }

        private val source: EnrolledAgent by lazy {
            val local =
                Chain.repository("/srv/$REPOSITORY") +
                    "secrets:\n  pg-app: ${PgAgent.SECRET_FILE}\n"
            val host = AgentHost(sard, "pg-source", local, image = E2e.pgAgentImage(18), mounts = listOf(Bind(repository, Volume("/srv/$REPOSITORY"))))
            val agent = EnrolledAgent(AgentEnroller.enroll(host, EnrollmentTokens.create(sard, role = "source")), host)
            val init = host.repoInit(REPOSITORY, "--generate-password")
            assertEquals(0, init.code, init.stderr)
            sard.secret(PG_PASSWORD)
            sard.track("agent-pg-source", AgentContainer.of(agent, mapOf(PgAgent.SECRET_FILE to "$PG_PASSWORD\n"))).apply { start() }
            Await.until("repository_id of $REPOSITORY in Register of pg-source") { Backups.repositoryId(sard, agent.agentId, REPOSITORY) != null }
            agent
        }

        /** A key of the repository issued on the source host for the verifier (V3). */
        private fun verifierKey(): String {
            val file = "${AgentHost.STATE_DIR}/issued-$REPOSITORY.pass"
            val add =
                source.host.run(
                    AgentImage.AGENT_BINARY, "repo", "key", "add", "--config", AgentHost.CONFIG, "--for", "verifier",
                    "--to-file", file, "--no-restart", REPOSITORY,
                    asRoot = true,
                )
            assertEquals(0, add.code, add.stderr)
            return String(source.host.read(file)).trimEnd('\n').also(sard::secret)
        }

        private val verifier by lazy {
            VerifierStand(sard, verifications).apply {
                val key = verifierKey()
                enroll(listOf(VerifierStand.Shared(REPOSITORY, VerifierStand.readOnly(repository), "${AgentHost.STATE_DIR}/verifier-main.pass", key)))
                start()
            }
        }

        /** The server, the source and the verifier, once for the class. */
        private fun stand() {
            db
            source
            verifier
        }
    }
}
