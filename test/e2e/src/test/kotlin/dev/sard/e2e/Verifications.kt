// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.e2e

import com.github.dockerjava.api.model.Bind
import com.github.dockerjava.api.model.Volume
import org.testcontainers.DockerClientFactory
import org.testcontainers.containers.GenericContainer
import org.testcontainers.containers.startupcheck.OneShotStartupCheckStrategy
import org.testcontainers.utility.DockerImageName
import java.security.MessageDigest
import java.sql.ResultSet
import java.time.Duration
import java.util.HexFormat
import java.util.UUID
import kotlin.test.assertEquals

/**
 * The verifications of a stand (V5): «verify now» through the REST API, the record the server keeps in
 * `restore_verifications`, the rows a test changes to forge a job, and the volumes a verifier works on.
 */
internal class Verifications(
    private val sard: SardEnvironment,
) {
    /** A verification as the server recorded it. */
    class Record(
        val status: String,
        val level: Int,
        val reason: String?,
        val stageNames: List<String>,
        val restoreMillis: Long?,
        /** The outputs of the stages, in order. */
        val outputs: List<String>,
    )

    fun sourceOf(snapshot: UUID): UUID = query("SELECT source_id FROM snapshots WHERE id = ?", snapshot) { it.getObject(1, UUID::class.java) }!!

    fun snapshotOfStep(step: UUID): UUID = query("SELECT id FROM snapshots WHERE step_id = ?", step) { it.getObject(1, UUID::class.java) }!!

    fun enable(
        sourceId: UUID,
        enabled: Boolean,
    ) {
        val set = SardApi(sard).put("/api/v1/sources/$sourceId/verification", """{"enabled":$enabled}""")
        assertEquals(SardApi.HTTP_OK, set.status, set.body)
    }

    /**
     * «Verify now» of [snapshot]; the job's id. A source whose verification is on (a system source of the
     * self-backup) may already have a job of the daily cycle open (409 verification_active): that job, if it is
     * of the same snapshot, is the one to wait for.
     */
    fun start(snapshot: UUID): UUID {
        val job = SardApi(sard).post("/api/v1/sources/${sourceOf(snapshot)}/verifications", """{"snapshotId":"$snapshot"}""")
        if (job.status == CONFLICT && job.field("code") == "verification_active") return openJobOf(snapshot)
        assertEquals(ACCEPTED, job.status, job.body)
        return UUID.fromString(job.require("id"))
    }

    private fun openJobOf(snapshot: UUID): UUID =
        checkNotNull(
            query("SELECT id FROM verification_jobs WHERE snapshot_ref = ? AND status <> 'closed'", snapshot) {
                it.getObject(1, UUID::class.java)
            },
        ) { "the open job of the source is not of snapshot $snapshot" }

    fun verify(
        snapshot: UUID,
        timeout: Duration = VERIFY_TIMEOUT,
    ): Record {
        val job = start(snapshot)
        return Await.value("the record of job $job", timeout) { recordOf(job) }
    }

    fun recordOf(job: UUID): Record? =
        query(
            """
            SELECT status, level, reason,
                (SELECT string_agg(s->>'name', ',' ORDER BY n) FROM jsonb_array_elements(stages) WITH ORDINALITY AS t(s, n)),
                restore_duration_ms,
                (SELECT string_agg(coalesce(s->>'output', ''), E'\x1f' ORDER BY n) FROM jsonb_array_elements(stages) WITH ORDINALITY AS t(s, n))
            FROM restore_verifications WHERE job_id = ?
            """.trimIndent(),
            job,
        ) {
            Record(
                it.getString(1), it.getInt(2), it.getString(3), it.getString(4)?.split(",").orEmpty(), it.getObject(5) as Long?,
                it.getString(6)?.split('\u001f').orEmpty(),
            )
        }

    fun update(
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

    fun <T> query(
        sql: String,
        vararg args: Any,
        read: (ResultSet) -> T,
    ): T? =
        sard.database().use { db ->
            db.prepareStatement(sql).use { st ->
                args.forEachIndexed { i, a -> st.setObject(i + 1, a) }
                st.executeQuery().use { rs -> if (rs.next()) read(rs) else null }
            }
        }

    /** Runs [script] as root in a throwaway container with [volume] at [path]; its output. */
    fun runOnVolume(
        volume: String,
        path: String,
        script: String,
    ): String =
        GenericContainer<Nothing>(DockerImageName.parse(E2e.agentImage)).use { c ->
            c.withCreateContainerCmdModifier { create ->
                create.withEntrypoint("/bin/sh", "-c", script).withUser("0")
                create.hostConfig?.withBinds(Bind(volume, Volume(path)))
            }
            c.withStartupCheckStrategy(OneShotStartupCheckStrategy().withTimeout(Duration.ofMinutes(1)))
            c.start()
            c.logs
        }

    /** A new volume owned by the agent's user, as a repository directory the operator made for it. */
    fun agentsVolume(): String =
        sard.volume().also { volume ->
            runOnVolume(volume, "/v", "chown ${TarFiles.AGENT_UID}:${TarFiles.AGENT_UID} /v && chmod 0700 /v")
        }

    /** A container that keeps the tmpfs of [work] mounted at /verify while a verifier restarts. */
    fun holder(work: String): GenericContainer<*> =
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

    /** The containers of verifications (label sard.verify.job) the stand's daemon has, running or not. */
    fun sandboxes(): List<String> =
        DockerClientFactory
            .instance()
            .client()
            .listContainersCmd()
            .withShowAll(true)
            .withLabelFilter(listOf(SANDBOX_LABEL))
            .exec()
            .map { it.names.joinToString() }

    /** The names of the volumes of the stand's daemon: a sandbox must not add one that outlives it. */
    fun volumes(): Set<String> =
        DockerClientFactory
            .instance()
            .client()
            .listVolumesCmd()
            .exec()
            .volumes
            .orEmpty()
            .map { it.name }
            .toSet()

    fun sha256(bytes: ByteArray): String = HexFormat.of().formatHex(MessageDigest.getInstance("SHA-256").digest(bytes))

    companion object {
        private const val ACCEPTED = 202
        private const val CONFLICT = 409
        const val SANDBOX_LABEL = "sard.verify.job"
        val VERIFY_TIMEOUT: Duration = Duration.ofMinutes(5)
    }
}
