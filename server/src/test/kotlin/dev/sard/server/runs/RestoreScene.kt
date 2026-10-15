// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.runs

import org.springframework.jdbc.core.JdbcTemplate
import java.sql.Timestamp
import java.time.Instant
import java.util.UUID

/**
 * The tenant of the restore scenarios (R2a): agent A (hostname db9, restore directory [DIR], repository "main"
 * and "zeta" with [RID], files with the restore schema), and helpers for more agents, sources and snapshots.
 */
internal class RestoreScene(
    private val jdbc: JdbcTemplate,
    private val sources: Sources,
    private val runs: Runs,
    private val queued: RecordingStepsQueued,
    val tenant: RunsTenant,
    val other: RunsTenant,
) {
    val a get() = tenant.agentId

    /** Agent A as the scenarios have it, and the other tenant. */
    fun create() {
        queued.calls.clear()
        tenant.create()
        other.create()
        jdbc.update(
            "update agents set hostname = 'db9', restore_dir = ?, secret_names = array['pg-app'] where id = ?",
            DIR,
            a,
        )
        jdbc.update("update agent_repositories set repository_id = ? where agent_id = ?", RID, a)
        jdbc.update(
            "insert into agent_repositories (tenant_id, agent_id, name, backend, repository_id) " +
                "values (?, ?, 'zeta', 'local', ?)",
            tenant.id,
            a,
            RID,
        )
        files(a)
    }

    fun drop() {
        jdbc.update("delete from snapshots where tenant_id in (?, ?)", tenant.id, other.id)
        tenant.drop()
        other.drop()
    }

    companion object {
        val SF = "4f1c2a9e" + "0".repeat(56)
        val SF_AT: Instant = Instant.parse("2026-10-10T03:00:00Z")
        const val SF_BYTES = 52_428_800L
        val RID = "a".repeat(64)
        val RID2 = "b".repeat(64)
        const val DIR = "/var/lib/sard-agent/restore"
        const val RESTORE_SCHEMA =
            """{"type":"object","properties":{"destination":{"type":"string","minLength":1},""" +
                """"paths":{"type":"array","items":{"type":"string"}},""" +
                """"token":{"type":"string","format":"sard-secret"}},"required":["destination"],""" +
                """"additionalProperties":false}"""
        const val DESTINATION = """{"destination":"etc%20data/20261010T030000Z"}"""
    }

    /** The agent offers the plugin [plugin], and the restore of it if [restore]. */
    fun files(
        agent: UUID,
        plugin: String = "files",
        restore: Boolean = true,
    ) {
        val count = "select count(*) from agent_plugins where agent_id = ? and name = ?"
        if (jdbc.queryForObject(count, Int::class.java, agent, plugin) == 0) {
            jdbc.update(
                "insert into agent_plugins (tenant_id, agent_id, name, version, config_schema, actions) " +
                    "values (?, ?, ?, '0.1.0', '{\"type\":\"object\"}'::jsonb, array['backup','restore'])",
                tenant.id,
                agent,
                plugin,
            )
        }
        jdbc.update(
            "update agent_plugins set restore_config_schema = ?::jsonb where agent_id = ? and name = ?",
            if (restore) RESTORE_SCHEMA else null,
            agent,
            plugin,
        )
    }

    /** Another agent of the tenant: [host], holding the repository [id] as [name]; it restores files unless told. */
    fun agent(
        host: String,
        name: String = "copy",
        id: String? = RID,
        dir: String? = DIR,
        plugins: List<String> = listOf("files"),
        restore: Boolean = true,
        revoked: Boolean = false,
    ): UUID {
        val agent = UUID.randomUUID()
        tenant.insertAgent(agent, plugins = emptyList(), repositories = listOf(name))
        jdbc.update("update agent_repositories set repository_id = ? where agent_id = ?", id, agent)
        jdbc.update(
            "update agents set hostname = ?, restore_dir = ?, secret_names = array['pg-app'] where id = ?",
            host,
            dir,
            agent,
        )
        if (revoked) jdbc.update("update agents set revoked_at = ? where id = ?", Timestamp.from(RUNS_NOW), agent)
        for (plugin in plugins) files(agent, plugin, restore)
        return agent
    }

    fun source(
        name: String = "etc data",
        plugin: String = "files",
    ): UUID {
        val draft = tenant.draft(name = name, plugin = plugin, config = """{"paths":["/etc"]}""")
        return sources.create(tenant.id, draft).id
    }

    /** A succeeded backup run of [source] with the snapshot [snapshot]. */
    fun snapshot(
        source: UUID,
        snapshot: String = SF,
        partial: Boolean = false,
        forgotten: Boolean = false,
        repositoryId: String = RID,
        agent: UUID = a,
    ) {
        val run = runs.start(tenant.id, source)
        val at = Timestamp.from(SF_AT)
        val status = if (partial) "failed" else "succeeded"
        jdbc.update(
            "update run_steps set status = ?, dispatched_at = ?, finished_at = ? where run_id = ?",
            status,
            at,
            at,
            run.id,
        )
        jdbc.update("update runs set status = ?, finished_at = ? where id = ?", status, at, run.id)
        jdbc.update(
            "insert into snapshots (id, tenant_id, source_id, step_id, agent_id, repository_name, repository_id, " +
                "snapshot_id, total_bytes, added_bytes, created_at, partial, forgotten_at) " +
                "select ?, tenant_id, source_id, id, ?, 'main', ?, ?, ?, 10, ?, ?, ? from run_steps where run_id = ?",
            UUID.randomUUID(),
            agent,
            repositoryId,
            snapshot,
            SF_BYTES,
            at,
            partial,
            if (forgotten) at else null,
            run.id,
        )
        queued.calls.clear() // the backup run was queued; the tests count the restores
    }
}
