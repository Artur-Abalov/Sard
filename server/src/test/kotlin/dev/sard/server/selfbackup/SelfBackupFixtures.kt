// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.selfbackup

import dev.sard.server.runs.RUNS_NOW
import org.springframework.jdbc.core.JdbcTemplate
import java.io.File
import java.sql.Timestamp
import java.util.UUID

private val POSTGRESQL_SCHEMA: String by lazy { File("../agent/plugins/postgresql/schema.json").readText() }
private val FILES_SCHEMA: String by lazy { File("../agent/plugins/files/schema.json").readText() }

internal const val S3_REPOSITORY = "self-s3"
internal const val SFTP_REPOSITORY = "self-sftp"
internal const val LOCAL_REPOSITORY = "self-local"
internal const val FRESH_REPOSITORY = "self-fresh"
internal const val S3_ID = "0a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f9"

/**
 * A tenant whose built-in agent offers the postgresql and files plugins, its secret and four repositories. [existing]
 * is a tenant that is there already, such as the installation's default one: it is not created and not dropped.
 */
internal class SelfBackupTenant(
    private val jdbc: JdbcTemplate,
    private val existing: UUID? = null,
) {
    val id: UUID = existing ?: UUID.randomUUID()
    val agentId: UUID = UUID.randomUUID()

    fun create(): SelfBackupTenant {
        if (existing == null) jdbc.update("insert into tenants (id, name) values (?, ?)", id, "self-$id")
        insertAgent(agentId)
        return this
    }

    /** A live built-in agent as its last Register leaves it; [secrets] are the names it holds. */
    fun insertAgent(
        agent: UUID,
        builtin: Boolean = true,
        secrets: List<String> = listOf("sard-db"),
    ) {
        jdbc.update(
            """
            insert into agents (id, tenant_id, hostname, registered_at, builtin, secret_names, role)
            values (?, ?, 'sard-self', ?, ?, ?, 'source')
            """.trimIndent(),
            agent,
            id,
            Timestamp.from(RUNS_NOW),
            builtin,
            secrets.toTypedArray(),
        )
        plugin(agent, "postgresql", POSTGRESQL_SCHEMA)
        plugin(agent, "files", FILES_SCHEMA)
        repository(agent, S3_REPOSITORY, "s3", S3_ID)
        repository(agent, SFTP_REPOSITORY, "sftp", S3_ID.reversed())
        repository(agent, LOCAL_REPOSITORY, "local", "f".repeat(64))
        repository(agent, FRESH_REPOSITORY, "s3", null)
    }

    /** Stands in for the agent's result: the run and its step end [status] at [at], with [message]. */
    fun finish(
        runId: UUID,
        status: String,
        at: java.time.Instant,
        message: String? = null,
    ) {
        val time = Timestamp.from(at)
        jdbc.update(
            "update run_steps set status = ?, dispatched_at = ?, finished_at = ?, message = ? where run_id = ?",
            status,
            time,
            time,
            message,
            runId,
        )
        val run = "update runs set status = ?, finished_at = ?, message = ? where id = ?"
        jdbc.update(run, status, time, message, runId)
    }

    fun revoke(agent: UUID) {
        jdbc.update("update agents set revoked_at = ? where id = ?", Timestamp.from(RUNS_NOW), agent)
    }

    private fun plugin(
        agent: UUID,
        name: String,
        schema: String,
    ) {
        jdbc.update(
            """
            insert into agent_plugins (tenant_id, agent_id, name, version, config_schema, actions)
            values (?, ?, ?, '0.1.0', ?::jsonb, array['backup','restore'])
            """.trimIndent(),
            id,
            agent,
            name,
            schema,
        )
    }

    private fun repository(
        agent: UUID,
        name: String,
        backend: String,
        repositoryId: String?,
    ) {
        jdbc.update(
            "insert into agent_repositories (tenant_id, agent_id, name, backend, repository_id) values (?, ?, ?, ?, ?)",
            id,
            agent,
            name,
            backend,
            repositoryId,
        )
    }

    fun count(table: String): Int? {
        val sql = "select count(*) from $table where tenant_id = ?"
        return jdbc.queryForObject(sql, Int::class.java, id)
    }

    fun drop() {
        val tables =
            listOf(
                "notification_deliveries",
                "no_verifier_episodes",
                "overdue_episodes",
                "schedule_fires",
                "run_steps",
                "runs",
                "schedules",
                "self_backups",
                "sources",
                "agent_plugins",
                "agent_repositories",
                "agents",
            )
        for (table in tables) {
            jdbc.update("delete from $table where tenant_id = ?", id)
        }
        if (existing == null) jdbc.update("delete from tenants where id = ?", id)
    }
}
