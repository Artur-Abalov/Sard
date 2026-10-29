// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.e2e

import java.sql.ResultSet
import java.time.Duration
import java.util.UUID

/**
 * Manual backups as the console starts them: a source and a run through the REST API (S8b), then
 * the server's own record of the step and its snapshot, read from its database.
 */
internal object Backups {
    private val ACTIVE = setOf("queued", "dispatched", "running")

    class Started(
        val sourceId: UUID,
        val runId: UUID,
        val stepId: UUID,
    )

    class Step(
        val status: String,
        val message: String?,
        val started: Boolean,
        val repositoryId: String?,
        val snapshotId: String?,
        val partial: Boolean?,
    )

    class Snapshot(
        val repositoryId: String,
        val snapshotId: String,
        val partial: Boolean,
    )

    /** Creates a source of [plugin] on [agentId] into repository [repository] with [configJson] and starts a run of it. */
    fun start(
        env: SardEnvironment,
        agentId: String,
        name: String,
        configJson: String,
        plugin: String = "files",
        repository: String = "main",
        backupTimeoutMinutes: Int? = null,
    ): Started {
        val api = SardApi(env)
        val timeout = backupTimeoutMinutes?.let { ""","backupTimeoutMinutes":$it""" }.orEmpty()
        val source =
            api.created(
                "/api/v1/sources",
                """{"name":"$name","agentId":"$agentId","plugin":"$plugin","repositoryName":"$repository","config":$configJson$timeout}""",
            )
        return run(env, UUID.fromString(source.require("id")), api)
    }

    /** Starts a run of the existing source [sourceId], as the console's "run now" does; the API answers 201. */
    fun run(
        env: SardEnvironment,
        sourceId: UUID,
        api: SardApi = SardApi(env),
    ): Started {
        val runId = UUID.fromString(api.created("/api/v1/sources/$sourceId/runs").require("id"))
        val stepId = Await.value("the step of run $runId") { query(env, "SELECT id FROM run_steps WHERE run_id = ?", runId) { it.getObject(1, UUID::class.java) } }
        return Started(sourceId, runId, stepId)
    }

    /**
     * Restores the snapshot [snapshotId] of source [sourceId] on [agentId] as the console's "Start the
     * restore" does (R2a); the API answers 201 with a restore run and its one step.
     */
    fun restore(
        env: SardEnvironment,
        sourceId: UUID,
        snapshotId: String,
        agentId: String,
        configJson: String,
        api: SardApi = SardApi(env),
    ): Started {
        val path = "/api/v1/sources/$sourceId/snapshots/$snapshotId/restores"
        val run = api.created(path, """{"agentId":"$agentId","config":$configJson}""")
        val runId = UUID.fromString(run.require("id"))
        val stepId = Await.value("the step of run $runId") { query(env, "SELECT id FROM run_steps WHERE run_id = ?", runId) { it.getObject(1, UUID::class.java) } }
        return Started(sourceId, runId, stepId)
    }

    /** A top-level field of the step's output as the server stored it (`output->>name`), or null. */
    fun outputField(
        env: SardEnvironment,
        stepId: UUID,
        name: String,
    ): String? = query(env, "SELECT output->>'$name' FROM run_steps WHERE id = ?", stepId) { it.getString(1) }

    /** Waits until the step is no longer active and returns the server's record of it. */
    fun awaitFinished(
        env: SardEnvironment,
        stepId: UUID,
        timeout: Duration = Await.TIMEOUT,
    ): Step = Await.value("a final status of step $stepId", timeout) { step(env, stepId)?.takeUnless { it.status in ACTIVE } }

    fun step(
        env: SardEnvironment,
        stepId: UUID,
    ): Step? =
        query(
            env,
            """
            SELECT status, message, started_at IS NOT NULL, output->>'repositoryId', output->>'snapshotId', (output->>'partial')::boolean
            FROM run_steps WHERE id = ?
            """.trimIndent(),
            stepId,
        ) { Step(it.getString(1), it.getString(2), it.getBoolean(3), it.getString(4), it.getString(5), it.getObject(6) as Boolean?) }

    fun runStatus(
        env: SardEnvironment,
        runId: UUID,
    ): String? = query(env, "SELECT status FROM runs WHERE id = ?", runId) { it.getString(1) }

    fun snapshot(
        env: SardEnvironment,
        stepId: UUID,
    ): Snapshot? =
        query(env, "SELECT repository_id, snapshot_id, partial FROM snapshots WHERE step_id = ?", stepId) {
            Snapshot(it.getString(1), it.getString(2), it.getBoolean(3))
        }

    /** A repository as the agent's last Register reported it; [id] is null while the agent could not read one. */
    class Repository(
        val backend: String,
        val id: String?,
    )

    fun repository(
        env: SardEnvironment,
        agentId: String,
        name: String,
    ): Repository? =
        env.database().use { connection ->
            connection.prepareStatement("SELECT backend, repository_id FROM agent_repositories WHERE agent_id = ? AND name = ?").use { q ->
                q.setObject(1, UUID.fromString(agentId))
                q.setString(2, name)
                q.executeQuery().use { if (it.next()) Repository(it.getString(1), it.getString(2)) else null }
            }
        }

    /** The repository_id the agent's last Register reported for [name], or null while it has none. */
    fun repositoryId(
        env: SardEnvironment,
        agentId: String,
        name: String,
    ): String? = repository(env, agentId, name)?.id

    /** Whether the agent has registered and has been seen on its stream. */
    fun connected(
        env: SardEnvironment,
        agentId: String,
    ): Boolean =
        query(env, "SELECT 1 FROM agents WHERE id = ? AND last_register_at IS NOT NULL AND last_seen_at IS NOT NULL", UUID.fromString(agentId)) {
            true
        } ?: false

    /** The bytes the step reported as processed (its last progress), 0 before any. */
    fun bytesProcessed(
        env: SardEnvironment,
        stepId: UUID,
    ): Long = query(env, "SELECT bytes_processed FROM run_steps WHERE id = ?", stepId) { it.getLong(1) } ?: 0

    /** The lines of the step's log on the server (`step_logs`), in order: what the console shows. */
    fun logs(
        env: SardEnvironment,
        stepId: UUID,
    ): List<String> =
        env.database().use { connection ->
            connection.prepareStatement("SELECT text FROM step_logs WHERE step_id = ? ORDER BY seq").use { q ->
                q.setObject(1, stepId)
                q.executeQuery().use { rows -> generateSequence { if (rows.next()) rows.getString(1) else null }.toList() }
            }
        }

    private fun <T> query(
        env: SardEnvironment,
        sql: String,
        id: UUID,
        read: (ResultSet) -> T,
    ): T? =
        env.database().use { connection ->
            connection.prepareStatement(sql).use { q ->
                q.setObject(1, id)
                q.executeQuery().use { if (it.next()) read(it) else null }
            }
        }
}
