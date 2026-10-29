// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import dev.sard.server.agents.stream.TestAgent
import dev.sard.server.registration.PluginAction
import dev.sard.server.registration.PluginEntry
import dev.sard.server.registration.RepositoryEntry
import dev.sard.server.role.AgentRole
import tools.jackson.databind.JsonNode
import java.sql.Timestamp
import java.time.Instant
import java.util.UUID

/** The restic repository id of the agents A and B ("RID" of the specification). */
val RID = "a".repeat(64)

/** The repository id of agent C ("RID2"). */
val RID2 = "b".repeat(64)

/** "Снимок SF": 64 hex characters starting with 4f1c2a9e. */
val SF = "4f1c2a9e" + "0".repeat(56)
val SF_AT: Instant = Instant.parse("2026-10-10T03:00:00Z")
const val SF_BYTES = 52_428_800L

/** A restore request body of the specification: "Запрос R(X)". */
fun restoreBody(
    agent: Any?,
    config: String = """{"destination": "etc%20data/20261010T030000Z"}""",
): String {
    val id = if (agent == null) "" else """"agentId": "$agent", """
    return """{$id"config": $config}"""
}

/**
 * The tenant of the restore scenarios: agent A (db1, repository "main" with [RID]), agent B (db2, repository
 * "copy" with [RID]) and agent C (db3, repository "other" with [RID2]), all with the restore of files; the source
 * S "etc data" of A with the snapshot SF from a succeeded run.
 */
class RestoreWorld(
    val world: RestWorld,
    val tenant: UUID,
    val admin: ApiSession,
) {
    private fun snapshot(
        repository: String,
        id: String,
        restoreDir: String = RESTORE_DIR,
        plugins: List<PluginEntry> = listOf(filesPlugin(), filesPlugin("mysql"), postgresqlPlugin()),
    ) = snapshotOf(
        plugins = plugins,
        repositories = listOf(RepositoryEntry(repository, "local", id, "")),
        secretNames = listOf("pg-app", "pg-restore"),
        restoreDir = restoreDir,
    )

    val a: TestAgent = world.agent(tenant, snapshot("main", RID), "db1")
    val b: TestAgent = world.agent(tenant, snapshot("copy", RID), "db2")
    val c: TestAgent = world.agent(tenant, snapshot("other", RID2), "db3")

    /** A verifier that holds [RID] too: never a restore target (R2a). */
    val v: TestAgent = world.agent(tenant, snapshot("verify", RID), "v1", role = AgentRole.VERIFIER)

    /** A source of [agent] on [repository]; its id. */
    fun source(
        name: String = "etc data",
        agent: TestAgent = a,
        repository: String = "main",
        plugin: String = "files",
        config: String = """{"paths":["/etc"]}""",
    ): String {
        val body =
            """{"name":"$name","agentId":"${agent.agentId}","plugin":"$plugin","repositoryName":"$repository",""" +
                """"config":$config}"""
        val created = world.api.post("/api/v1/sources", admin, body)
        check(created.status == 201) { "source: $created" }
        return created.json.path("id").asString()
    }

    /** A backup run of [source] that succeeded with the snapshot [snapshotId]; the id of its run and step. */
    fun snapshot(
        source: String,
        snapshotId: String = SF,
        repositoryId: String = RID,
        repositoryName: String = "main",
        at: Instant = SF_AT,
        partial: Boolean = false,
        forgotten: Boolean = false,
    ): Pair<String, String> {
        val run = world.api.post("/api/v1/sources/$source/runs", admin, null)
        check(run.status == 201) { "run: $run" }
        val runId = run.json.path("id").asString()
        val step =
            run.json
                .path("steps")
                .get(0)
                .path("id")
                .asString()
        world.forceRun(UUID.fromString(runId), if (partial) "failed" else "succeeded")
        val output =
            """{"kind":"backup","snapshotId":"$snapshotId","totalBytes":$SF_BYTES,"addedBytes":10,""" +
                """"repositoryId":"$repositoryId","partial":$partial}"""
        world.jdbc.update("update run_steps set output = ?::jsonb where id = ?", output, UUID.fromString(step))
        world.jdbc.update(
            "insert into snapshots (id, tenant_id, source_id, step_id, agent_id, repository_name, repository_id, " +
                "snapshot_id, total_bytes, added_bytes, created_at, partial, incomplete_reason, forgotten_at) " +
                "select ?, tenant_id, source_id, id, agent_id, ?, ?, ?, ?, 10, ?, ?, ?, ? " +
                "from run_steps where id = ?",
            UUID.randomUUID(),
            repositoryName,
            repositoryId,
            snapshotId,
            SF_BYTES,
            Timestamp.from(at),
            partial,
            if (partial) "unknown" else null,
            if (forgotten) Timestamp.from(at) else null,
            UUID.fromString(step),
        )
        return runId to step
    }

    fun options(
        source: String,
        snapshot: String = SF,
        session: ApiSession? = admin,
    ) = world.api.get("/api/v1/sources/$source/snapshots/$snapshot/restore-options", session)

    fun preview(
        source: String,
        body: String,
        snapshot: String = SF,
        session: ApiSession? = admin,
    ) = world.api.send("POST", "/api/v1/sources/$source/snapshots/$snapshot/restores/preview", session, body)

    fun restore(
        source: String,
        body: String,
        snapshot: String = SF,
        session: ApiSession? = admin,
        headers: Map<String, String> = emptyMap(),
    ) = world.api.send("POST", "/api/v1/sources/$source/snapshots/$snapshot/restores", session, body, headers)

    fun runs(query: String = ""): JsonNode = world.api.get("/api/v1/runs$query", admin).json

    fun restoreRuns(): Int {
        val sql = "select count(*) from runs where kind = 'restore'"
        return world.jdbc.queryForObject(sql, Int::class.java)!!
    }

    fun revoke(agent: TestAgent) = world.api.post("/api/v1/agents/${agent.agentId}/revoke", admin, null)

    /** The agent registers again with [plugins] and [repositories]. */
    fun reRegister(
        agent: TestAgent,
        repositories: List<RepositoryEntry>,
        plugins: List<PluginEntry> = listOf(filesPlugin()),
        hostname: String = "db2",
    ) = world.register(
        agent,
        snapshotOf(plugins, repositories, listOf("pg-app", "pg-restore")).copy(hostname = hostname),
    )

    fun plugin(actions: List<PluginAction?>) = filesPlugin(actions = actions)
}
