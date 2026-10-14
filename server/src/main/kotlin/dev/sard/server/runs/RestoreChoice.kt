// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.runs

import dev.sard.server.persistence.Agent
import dev.sard.server.persistence.AgentOwnedKey
import dev.sard.server.persistence.AgentPluginRecord
import dev.sard.server.persistence.AgentRepositoryRecord
import dev.sard.server.persistence.SnapshotRecord
import dev.sard.server.persistence.SourceRecord
import jakarta.persistence.LockModeType
import org.hibernate.Session
import tools.jackson.databind.JsonNode
import tools.jackson.databind.json.JsonMapper
import java.util.UUID

private val JSON = JsonMapper.builder().build()

private const val SNAPSHOT_OF_SOURCE = "from SnapshotRecord where sourceId = :source and snapshotId = :snapshot"
private const val REPOSITORIES_WITH_ID =
    "from AgentRepositoryRecord where agentId = :agent and repositoryId = :id order by name"

/** A restore request resolved against the snapshot and the agent's last Register (ВК33). */
internal class RestoreChoice(
    val source: SourceRecord,
    val snapshot: SnapshotRecord,
    val agent: Agent,
    /** The repository on [agent] that holds the snapshot. */
    val repositoryName: String,
    val config: JsonNode,
) {
    /** The restore directory with the destination of a files restore, as the agent will write it (ВК32). */
    val target: String =
        agent.restoreDir
            .orEmpty()
            .trimEnd('/') + "/" + config.path("destination").asString()

    val paths: List<String> =
        config
            .path("paths")
            .iterator()
            .asSequence()
            .map { it.asString() }
            .toList()

    companion object {
        /**
         * The request checked in the order of ВК33, each refusal the first that applies: the snapshot of the
         * source (404), whether the console restores it (422), the agent (422, 409), its repository and plugin
         * (409), the config against the agent's restore schema and secrets (422).
         */
        fun resolve(
            session: Session,
            sourceId: UUID,
            snapshotId: String,
            draft: RestoreDraft,
            lock: LockModeType,
        ): RestoreChoice {
            val (source, snapshot) = snapshotOf(session, sourceId, snapshotId)
            RestoreSupport.blocked(source.plugin, snapshot.partial, snapshot.forgottenAt != null)?.let {
                throw SnapshotNotRestorable(it)
            }
            val agent = agentOf(session, draft.agentId, lock)
            val repository = repositoryOf(session, agent, snapshot)
            val config = configOf(session, agent, source, draft)
            return RestoreChoice(source, snapshot, agent, repository, config)
        }

        /** The source (deleted or not) and its snapshot with restic's id [snapshotId]; 404 for either missing. */
        fun snapshotOf(
            session: Session,
            sourceId: UUID,
            snapshotId: String,
        ): Pair<SourceRecord, SnapshotRecord> {
            val missing = SnapshotNotFound(sourceId, snapshotId)
            val source = session.find(SourceRecord::class.java, sourceId) ?: throw missing
            val snapshot =
                session
                    .createSelectionQuery(SNAPSHOT_OF_SOURCE, SnapshotRecord::class.java)
                    .setParameter("source", sourceId)
                    .setParameter("snapshot", snapshotId)
                    .resultList
                    .firstOrNull() ?: throw missing
            return source to snapshot
        }

        private fun agentOf(
            session: Session,
            agentId: String?,
            lock: LockModeType,
        ): Agent {
            val id = agentId?.let { runCatching { UUID.fromString(it) }.getOrNull() }
            val agent = id?.let { session.find(Agent::class.java, it, lock) } ?: throw RestoreAgentInvalid()
            return agent.also { if (it.revokedAt != null) throw AgentRevoked(agent.id) }
        }

        private fun repositoryOf(
            session: Session,
            agent: Agent,
            snapshot: SnapshotRecord,
        ): String =
            session
                .createSelectionQuery(REPOSITORIES_WITH_ID, AgentRepositoryRecord::class.java)
                .setParameter("agent", agent.id)
                .setParameter("id", snapshot.repositoryId)
                .resultList
                .firstOrNull()
                ?.name ?: throw UnknownRepository(snapshot.repositoryName)

        private fun configOf(
            session: Session,
            agent: Agent,
            source: SourceRecord,
            draft: RestoreDraft,
        ): JsonNode {
            val schema = restoreSchema(session, agent, source.plugin) ?: throw UnknownPlugin(source.plugin)
            val secrets = agent.secretNames.toSet()
            val violations = ConfigCheck.violations(schema, draft.config, secrets, namesMissing = true)
            if (violations.isNotEmpty()) throw RestoreConfigInvalid(violations)
            return JSON.readTree(draft.config)
        }

        /** The restore schema the agent announced for [plugin]; null when it cannot restore it from the console. */
        fun restoreSchema(
            session: Session,
            agent: Agent,
            plugin: String,
        ): String? =
            agent.restoreDir?.let {
                session.find(AgentPluginRecord::class.java, AgentOwnedKey(agent.id, plugin))?.restoreConfigSchema
            }
    }
}
