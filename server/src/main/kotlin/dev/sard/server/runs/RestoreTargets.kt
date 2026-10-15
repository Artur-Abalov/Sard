// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.runs

import dev.sard.server.persistence.Agent
import dev.sard.server.persistence.AgentOwnedKey
import dev.sard.server.persistence.AgentPluginRecord
import dev.sard.server.persistence.SnapshotRecord
import dev.sard.server.persistence.SourceRecord
import org.hibernate.Session
import tools.jackson.databind.json.JsonMapper

private val JSON = JsonMapper.builder().build()

private const val HOLDERS =
    "select a, r.name from Agent a, AgentRepositoryRecord r " +
        "where r.agentId = a.id and r.repositoryId = :repository and a.revokedAt is null order by r.name"
private const val ADDRESS = "адрес хранилища"
private const val PASSWORD_FILE = "файл с копией пароля"

/** The agents that hold the repository of a snapshot, split into those that restore it and those that cannot (ВК30). */
internal object RestoreTargets {
    fun of(
        session: Session,
        source: SourceRecord,
        snapshot: SnapshotRecord,
    ): RestoreOptions {
        val holders = holders(session, snapshot)
        val targets = holders.mapNotNull { (agent, repository) -> target(session, agent, repository, source, snapshot) }
        val unable = holders.filter { (agent, _) -> targets.none { it.agentId == agent.id } }
        return RestoreOptions(
            snapshot =
                RestoreSnapshot(
                    snapshot.snapshotId,
                    snapshot.createdAt,
                    snapshot.totalBytes,
                    snapshot.repositoryName,
                    source.name,
                    source.plugin,
                ),
            targets = targets.sortedWith(compareBy({ !it.sourceAgent }, { it.hostname }, { it.agentId })),
            ineligible = unable.map { (agent, _) -> ineligible(session, agent, source) }.sortedBy { it.hostname },
            attachRepositoryName = snapshot.repositoryName,
            attachCommand = attachCommand(snapshot.repositoryName),
        )
    }

    /** The command to attach the repository on another host: placeholders, never an address (ADR 0008, ВК31). */
    private fun attachCommand(repository: String) = "sudo sard-agent repo add $repository <$ADDRESS> --password-from-file <$PASSWORD_FILE>"

    /** Each unrevoked agent that holds the repository once, with its first name for it. */
    private fun holders(
        session: Session,
        snapshot: SnapshotRecord,
    ): List<Pair<Agent, String>> =
        session
            .createSelectionQuery(HOLDERS, Array<Any?>::class.java)
            .setParameter("repository", snapshot.repositoryId)
            .resultList
            .map { it[0] as Agent to it[1] as String }
            .distinctBy { it.first.id }

    private fun target(
        session: Session,
        agent: Agent,
        repository: String,
        source: SourceRecord,
        snapshot: SnapshotRecord,
    ): RestoreTarget? {
        val schema = RestoreChoice.restoreSchema(session, agent, source.plugin) ?: return null
        return RestoreTarget(
            agent.id,
            agent.hostname,
            agent.id == snapshot.agentId,
            repository,
            agent.restoreDir.orEmpty(),
            schema,
            defaults(source, snapshot),
        )
    }

    /** The config the form starts from: a files restore goes to `<source name>/<snapshot time>`. */
    private fun defaults(
        source: SourceRecord,
        snapshot: SnapshotRecord,
    ): String {
        val config =
            if (source.plugin == FILES_PLUGIN) {
                mapOf("destination" to RestoreDefaults.destination(source.name, snapshot.createdAt))
            } else {
                emptyMap()
            }
        return JSON.writeValueAsString(config)
    }

    private fun ineligible(
        session: Session,
        agent: Agent,
        source: SourceRecord,
    ): IneligibleAgent {
        val offers = session.find(AgentPluginRecord::class.java, AgentOwnedKey(agent.id, source.plugin)) != null
        val reason = if (offers) IneligibleReason.AGENT_OUTDATED else IneligibleReason.PLUGIN_MISSING
        return IneligibleAgent(agent.id, agent.hostname, reason)
    }
}
