// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.e2e

import com.github.dockerjava.api.model.AccessMode
import com.github.dockerjava.api.model.Bind
import com.github.dockerjava.api.model.Volume
import org.junit.jupiter.api.extension.RegisterExtension
import org.testcontainers.containers.GenericContainer
import org.testcontainers.utility.DockerImageName
import java.security.MessageDigest
import java.time.Duration
import java.util.HexFormat
import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertContains
import kotlin.test.assertEquals
import kotlin.test.assertNotNull
import kotlin.test.assertTrue

/**
 * V5, phase 2: a verifier checks snapshots of the files plugin on the stand (strategy of the task, checks 2, 3, 6,
 * 7, 8 and check 1 for files). A source host S and a verifier V share a local repository on a volume: S mounts it
 * read and write, V read only, as storage credentials that only read. V restores into a work volume mounted noexec
 * (tmpfs), kept by a holder container across a restart of V. Every verification is started with «verify now» and
 * read from `restore_verifications`.
 */
class VerificationFilesTest {
    // Check 1 for files, check 6: every stage passes, level 5, and the repository holds the same files after.
    @Test
    fun `a snapshot of files passes every stage to level 5 and nothing is written to the repository`() {
        stand()
        val snapshot = backup("passing")
        val before = repositoryListing()

        val record = verify(snapshot)

        assertEquals("passed", record.status, record.reason)
        assertEquals(5, record.level)
        assertEquals(FILES_STAGES, record.stageNames)
        assertTrue(record.restoreMillis!! > 0, "no restore time")
        assertEquals(before, repositoryListing(), "the verification wrote to the repository")
        assertEquals(emptyList(), workVolume(), "the verification left a copy on the verifier")
        assertNotNull(SardApi(sard).get("/api/v1/status").field("lastVerifiedRestoreAt"))
    }

    // Check 2: the fingerprint the server sends is not the one the backup tagged.
    @Test
    fun `a fingerprint changed on the server fails before anything is read`() {
        stand()
        val snapshot = backup("forged")
        val forged = """{"facts":[{"name":"files","rule":"exact","value":"1"}],"format":1,"plugin":"files"}""".toByteArray()
        update("UPDATE snapshots SET fingerprint = ?, fingerprint_sha256 = ? WHERE id = ?", forged, sha256(forged), snapshot)

        val record = verify(snapshot)

        assertEquals("failed", record.status)
        assertEquals(0, record.level)
        assertEquals(listOf("snapshot", "fingerprint_tag", "cleanup"), record.stageNames)
        assertContains(record.reason.orEmpty(), "fingerprint does not match the snapshot")
    }

    // Check 2: a snapshot from before fingerprints is restored, and its data cannot be compared.
    @Test
    fun `a snapshot without a fingerprint reaches level 3 and cannot be verified`() {
        stand()
        val snapshot = backup("pre-v2")
        update("UPDATE snapshots SET fingerprint = NULL, fingerprint_sha256 = NULL WHERE id = ?", snapshot)

        val record = verify(snapshot)

        assertEquals("cannot_verify", record.status)
        assertEquals(3, record.level)
        assertContains(record.reason.orEmpty(), "no fingerprint")
        assertEquals(emptyList(), workVolume())
    }

    // Check 3: a damaged pack fails at level 1 (verify.check_percent 100 reads every pack).
    @Test
    fun `a damaged pack fails at level 1`() {
        stand()
        val snapshot = backup("damaged", repository = SPARE)
        val damaged =
            helper(spare, "/repo", "for p in /repo/data/*/*; do printf XXXXXXXXXXXXXXXX | dd of=\$p bs=1 seek=64 conv=notrunc 2>/dev/null; done; echo done")
        assertContains(damaged, "done")

        val record = verify(snapshot)

        assertEquals("failed", record.status)
        assertEquals(0, record.level)
        assertEquals(listOf("snapshot", "fingerprint_tag", "work_volume", "restic_check", "cleanup"), record.stageNames)
        assertEquals(emptyList(), workVolume())
    }

    // Check 7: a verifier killed in the middle of a restore removes the copy when it starts again.
    @Test
    fun `a verification killed in the middle leaves nothing after the verifier restarts`() {
        stand()
        val many = (1..MANY_FILES).map { TarFiles.Entry("f$it", ByteArray(FILE_BYTES) { b -> (b * it).toByte() }, TarFiles.OWNER_ONLY) }
        source.host.put(MANY, TarFiles(listOf(TarFiles.Entry("", null, TarFiles.OWNER_DIR)) + many))
        val snapshot = backup("killed", paths = MANY)
        val job = start(snapshot)
        Await.until("a copy on the work volume", Duration.ofMinutes(2)) { workVolume().isNotEmpty() }

        val killed = checkNotNull(verifierContainer)
        killed.dockerClient.killContainerCmd(killed.containerId).exec()
        verifierContainer = startVerifier("verifier-restarted")

        assertEquals(emptyList(), workVolume(), "the start did not remove what the killed verification left")
        Await.until("job $job settled", VERIFY_TIMEOUT) { recordOf(job) != null }
        assertEquals(emptyList(), workVolume())
    }

    // Check 8: the server is made to send the verifier a source of its own machine; the verifier refuses every attempt.
    @Test
    fun `a verifier refuses a source of its own machine`() {
        stand()
        val snapshot = backup("own-machine")
        val own = query("SELECT machine_id FROM agents WHERE id = ?::uuid", verifier.agentId) { it.getString(1) }!!
        val sourceMachine = query("SELECT machine_id FROM agents WHERE id = ?::uuid", source.agentId) { it.getString(1) }!!
        update("UPDATE agents SET machine_id = ? WHERE id = ?::uuid", own, source.agentId)
        update("UPDATE agents SET machine_id = ? WHERE id = ?::uuid", "f".repeat(64), verifier.agentId)
        try {
            val record = verify(snapshot)

            assertEquals("infrastructure_failure", record.status)
            assertContains(record.reason.orEmpty(), "own machine")
        } finally {
            update("UPDATE agents SET machine_id = ? WHERE id = ?::uuid", sourceMachine, source.agentId)
            update("UPDATE agents SET machine_id = ? WHERE id = ?::uuid", own, verifier.agentId)
        }
    }

    /** A verification as the server recorded it. */
    private class Record(
        val status: String,
        val level: Int,
        val reason: String?,
        val stageNames: List<String>,
        val restoreMillis: Long?,
    )

    /** Backs up [paths] as a new source [name] into [repository] and returns its snapshot's id (snapshots.id). */
    private fun backup(
        name: String,
        repository: String = Chain.REPOSITORY,
        paths: String = Chain.DATA,
    ): UUID {
        val config = if (paths == Chain.DATA) Chain.files(paths, TREE.excludePatterns(paths)) else Chain.files(paths)
        val started = Backups.start(sard, source.agentId, name, config, repository = repository)
        val step = Backups.awaitFinished(sard, started.stepId)
        assertEquals("succeeded", step.status, step.message)
        val api = SardApi(sard)
        val enabled = api.put("/api/v1/sources/${started.sourceId}/verification", """{"enabled":true}""")
        assertEquals(SardApi.HTTP_OK, enabled.status, enabled.body)
        return query("SELECT id FROM snapshots WHERE step_id = ?", started.stepId) { it.getObject(1, UUID::class.java) }!!
    }

    /** «Verify now» of [snapshot]; the job's id. */
    private fun start(snapshot: UUID): UUID {
        val sourceId = query("SELECT source_id FROM snapshots WHERE id = ?", snapshot) { it.getObject(1, UUID::class.java) }!!
        val job = SardApi(sard).post("/api/v1/sources/$sourceId/verifications", """{"snapshotId":"$snapshot"}""")
        assertEquals(ACCEPTED, job.status, job.body)
        return UUID.fromString(job.require("id"))
    }

    private fun verify(snapshot: UUID): Record {
        val job = start(snapshot)
        return Await.value("the record of job $job", VERIFY_TIMEOUT) { recordOf(job) }
    }

    private fun recordOf(job: UUID): Record? =
        query(
            """
            SELECT status, level, reason, (SELECT string_agg(s->>'name', ',' ORDER BY n) FROM jsonb_array_elements(stages) WITH ORDINALITY AS t(s, n)),
                restore_duration_ms
            FROM restore_verifications WHERE job_id = ?
            """.trimIndent(),
            job,
        ) { Record(it.getString(1), it.getInt(2), it.getString(3), it.getString(4)?.split(",").orEmpty(), it.getObject(5) as Long?) }

    /** Every file of the shared repository with its size and time of change, sorted. */
    private fun repositoryListing(): String = helper(repository, "/repo", "find /repo -type f -printf '%p %s %T@\\n' | sort")

    /** The names on the verifier's work volume, as the holder of the volume sees them. */
    private fun workVolume(): List<String> = holder.execInContainer("ls", "-A", "/verify").stdout.lines().filter { it.isNotBlank() }

    /** Runs [script] in a throwaway container with [volume] at [path]; its output. */
    private fun helper(
        volume: String,
        path: String,
        script: String,
    ): String =
        GenericContainer<Nothing>(DockerImageName.parse(E2e.agentImage)).use { c ->
            c.withCreateContainerCmdModifier { create ->
                create.withEntrypoint("/bin/sh", "-c", script).withUser("0")
                create.hostConfig?.withBinds(Bind(volume, Volume(path)))
            }
            c.withStartupCheckStrategy(org.testcontainers.containers.startupcheck.OneShotStartupCheckStrategy().withTimeout(Duration.ofMinutes(1)))
            c.start()
            c.logs
        }

    private fun update(
        sql: String,
        vararg args: Any,
    ) {
        sard.database().use { db ->
            db.prepareStatement(sql).use { st ->
                args.forEachIndexed { i, a -> st.setObject(i + 1, a) }
                st.executeUpdate()
            }
        }
    }

    private fun <T> query(
        sql: String,
        vararg args: Any,
        read: (java.sql.ResultSet) -> T,
    ): T? =
        sard.database().use { db ->
            db.prepareStatement(sql).use { st ->
                args.forEachIndexed { i, a -> st.setObject(i + 1, a) }
                st.executeQuery().use { rs -> if (rs.next()) read(rs) else null }
            }
        }

    private fun sha256(bytes: ByteArray): String = HexFormat.of().formatHex(MessageDigest.getInstance("SHA-256").digest(bytes))

    companion object {
        @JvmField
        @RegisterExtension
        val sard = SardEnvironment()

        private const val SPARE = "spare"
        private const val ACCEPTED = 202
        private const val MANY_FILES = 20_000
        private const val FILE_BYTES = 2048
        private const val MANY = "${AgentHost.STATE_DIR}/many"
        private val VERIFY_TIMEOUT: Duration = Duration.ofMinutes(5)
        private val TREE = SourceTree.generate(Chain.SEED)
        private val FILES_STAGES =
            listOf("snapshot", "fingerprint_tag", "work_volume", "restic_check", "restore", "start", "fingerprint", "cleanup")

        private val repository by lazy { sard.volume() }
        private val spare by lazy { sard.volume() }
        private val work by lazy { sard.noexecVolume() }

        /** The repositories of a host: [repository] as `main`, [spare] as `spare`, under /srv. */
        private fun repositories(
            mainPassword: String,
            sparePassword: String,
        ) = """
            repositories:
              - {name: ${Chain.REPOSITORY}, url: /srv/main, password_file: $mainPassword}
              - {name: $SPARE, url: /srv/spare, password_file: $sparePassword}
            """.trimIndent() + "\n"

        private val source: EnrolledAgent by lazy {
            val host =
                AgentHost(
                    sard, "verify-source", repositories(Chain.PASSWORD_FILE, "${AgentHost.STATE_DIR}/$SPARE.pass"),
                    mounts = listOf(Bind(repository, Volume("/srv/main")), Bind(spare, Volume("/srv/spare"))),
                )
            val agent = EnrolledAgent(AgentEnroller.enroll(host, EnrollmentTokens.create(sard, role = "source")), host)
            host.put(Chain.DATA, TREE.files())
            val spareInit = host.repoInit(SPARE, "--generate-password")
            assertEquals(0, spareInit.code, spareInit.stderr)
            Chain.ready(sard, agent)
            agent
        }

        private const val MAIN_KEY = "${AgentHost.STATE_DIR}/verifier-main.pass"
        private const val SPARE_KEY = "${AgentHost.STATE_DIR}/verifier-spare.pass"

        /** A key of [repo] issued on the source host for the verifier (V3). */
        private fun verifierKey(repo: String): String {
            val file = "${AgentHost.STATE_DIR}/issued-$repo.pass"
            val add =
                source.host.run(
                    AgentImage.AGENT_BINARY, "repo", "key", "add", "--config", AgentHost.CONFIG, "--for", "verifier",
                    "--to-file", file, "--no-restart", repo,
                    asRoot = true,
                )
            assertEquals(0, add.code, add.stderr)
            return String(source.host.read(file)).trimEnd('\n').also(sard::secret)
        }

        private val keys: Map<String, String> by lazy { mapOf(MAIN_KEY to verifierKey(Chain.REPOSITORY), SPARE_KEY to verifierKey(SPARE)) }

        private val verifier: EnrolledAgent by lazy {
            val local = repositories(MAIN_KEY, SPARE_KEY) + "verify:\n  work_dir: /verify\n  check_percent: 100\n"
            val host =
                AgentHost(
                    sard, "verify-verifier", local,
                    mounts =
                        listOf(
                            Bind(repository, Volume("/srv/main"), AccessMode.ro),
                            Bind(spare, Volume("/srv/spare"), AccessMode.ro),
                            Bind(work, Volume("/verify")),
                        ),
                )
            EnrolledAgent(AgentEnroller.enroll(host, EnrollmentTokens.create(sard, role = "verifier")), host)
        }

        /** Keeps the tmpfs of the work volume mounted while the verifier restarts. */
        private val holder by lazy {
            sard
                .track(
                    "verify-holder",
                    GenericContainer<Nothing>(DockerImageName.parse(E2e.agentImage)).apply {
                        withCreateContainerCmdModifier { create ->
                            create.withEntrypoint("/bin/sleep", "infinity").withUser("0")
                            create.hostConfig?.withBinds(Bind(work, Volume("/verify")))
                        }
                    },
                ).apply { start() }
        }

        private fun startVerifier(name: String): GenericContainer<*> {
            val container = sard.track(name, AgentContainer.of(verifier, keys)).apply { start() }
            Await.until("the verifier online with the repositories") {
                listOf(Chain.REPOSITORY, SPARE).all { Backups.repositoryId(sard, verifier.agentId, it) != null }
            }
            return container
        }

        private var verifierContainer: GenericContainer<*>? = null

        /** The source, the verifier and the holder of its work volume, once for the class. */
        private fun stand() {
            source
            holder
            if (verifierContainer == null) verifierContainer = startVerifier("verifier")
        }
    }
}
