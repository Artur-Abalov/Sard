// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.runs

import dev.sard.server.pki.MovableClock
import org.springframework.boot.test.context.TestConfiguration
import org.springframework.context.annotation.Bean
import org.springframework.context.annotation.Primary
import org.springframework.jdbc.core.JdbcTemplate
import java.time.Duration
import java.time.Instant
import java.util.UUID
import java.util.concurrent.CopyOnWriteArrayList

internal val RUNS_NOW: Instant = Instant.parse("2026-09-30T10:00:00Z")

/** How long a test waits for racing threads before giving up. */
internal val RUNS_RACE_WAIT: Duration = Duration.ofSeconds(20)

private val AGENT_TABLES = listOf("agent_plugins", "agent_repositories", "agents")

internal const val PLUGIN = "postgresql"
internal const val REPOSITORY = "main"
internal const val CONFIG = """{"database": "app", "password": {"secret": "pg-prod"}}"""

/** A tenant with one registered agent offering [PLUGIN] and [REPOSITORY], as Register leaves it. */
internal class RunsTenant(
    private val jdbc: JdbcTemplate,
) {
    val id: UUID = UUID.randomUUID()
    val agentId: UUID = UUID.randomUUID()

    fun create(): RunsTenant {
        jdbc.update("insert into tenants (id, name) values (?, ?)", id, "runs-$id")
        insertAgent(agentId)
        return this
    }

    fun insertAgent(
        agent: UUID,
        plugins: List<String> = listOf(PLUGIN),
        repositories: List<String> = listOf(REPOSITORY),
    ) {
        jdbc.update(
            "insert into agents (id, tenant_id, hostname, registered_at) values (?, ?, 'db1', ?)",
            agent,
            id,
            java.sql.Timestamp.from(RUNS_NOW),
        )
        for (plugin in plugins) {
            jdbc.update(
                """
                insert into agent_plugins (tenant_id, agent_id, name, version, config_schema, actions)
                values (?, ?, ?, '0.1.0', '{"type":"object"}'::jsonb, array['backup','restore'])
                """.trimIndent(),
                id,
                agent,
                plugin,
            )
        }
        for (repository in repositories) {
            jdbc.update(
                "insert into agent_repositories (tenant_id, agent_id, name, backend) values (?, ?, ?, 'local')",
                id,
                agent,
                repository,
            )
        }
    }

    fun draft(
        name: String = "prod-db",
        agent: UUID = agentId,
        plugin: String = PLUGIN,
        repository: String = REPOSITORY,
        config: String = CONFIG,
    ) = SourceDraft(name, agent, plugin, repository, config)

    fun count(table: String): Int? {
        val sql = "select count(*) from $table where tenant_id = ?"
        return jdbc.queryForObject(sql, Int::class.java, id)
    }

    /** Stands in for S7: the step and its run reach a final state, as a StepResult would leave them. */
    fun finish(runId: UUID) {
        val at = java.sql.Timestamp.from(RUNS_NOW)
        jdbc.update(
            "update run_steps set status = 'succeeded', dispatched_at = ?, finished_at = ? where run_id = ?",
            at,
            at,
            runId,
        )
        jdbc.update("update runs set status = 'succeeded', finished_at = ? where id = ?", at, runId)
    }

    fun drop() {
        val tables = listOf("run_steps", "runs", "sources", "workflows") + AGENT_TABLES
        for (table in tables) {
            jdbc.update("delete from $table where tenant_id = ?", id)
        }
        jdbc.update("delete from tenants where id = ?", id)
    }
}

/** Records every [StepsQueued.onQueued] call instead of dispatching. */
class RecordingStepsQueued : StepsQueued {
    val calls = CopyOnWriteArrayList<Pair<UUID, UUID>>()

    override fun onQueued(
        tenantId: UUID,
        agentId: UUID,
    ) {
        calls += tenantId to agentId
    }
}

@TestConfiguration(proxyBeanMethods = false)
class RunsTestConfiguration {
    @Bean
    fun clock(): MovableClock = MovableClock(RUNS_NOW)

    @Bean
    @Primary
    fun recordingStepsQueued() = RecordingStepsQueued()
}
