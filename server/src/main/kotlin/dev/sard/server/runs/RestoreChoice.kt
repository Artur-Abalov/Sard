// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.runs

import dev.sard.server.persistence.Agent
import dev.sard.server.persistence.AgentOwnedKey
import dev.sard.server.persistence.AgentPluginRecord
import dev.sard.server.persistence.AgentRepositoryRecord
import dev.sard.server.persistence.RunStepRecord
import dev.sard.server.persistence.SnapshotRecord
import dev.sard.server.persistence.SourceRecord
import dev.sard.server.role.AgentRole
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
    /** What the console knows of the restore of the plugin of the source (ВК32a); the rest reads no key of a plugin. */
    private val plugin: RestorePlugin,
    /** The copy of the config of the step that made the snapshot. */
    private val backupConfig: JsonNode,
) {
    /** Where the restore goes: an absolute path of the restore directory, or the name of the database (ВК32). */
    val target: String = plugin.target(agent.restoreDir.orEmpty(), config)

    val paths: List<String> = plugin.paths(config)

    val restoreGlobals: Boolean = plugin.restoreGlobals(config)

    /** The agent that restores is the agent that made the backup (ВК32b). */
    val warnings: List<RestoreWarning> = plugin.warnings(config, backupConfig, agent.id == snapshot.agentId)

    companion object {
        /**
         * The request checked in the order of ВК33, each refusal the first that applies: the snapshot of the
         * source (404), whether the console restores it (422), the agent (422, 409: unknown, revoked, then a role
         * that cannot restore), its repository and plugin (409), the config against the agent's restore schema and
         * secrets (422).
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
            val plugin = checkNotNull(RestorePlugins.of(source.plugin)) { "only the registry's plugins pass blocked()" }
            val agent = agentOf(session, draft.agentId, lock)
            val repository = repositoryOf(session, agent, snapshot)
            val config = configOf(session, agent, source, draft)
            return RestoreChoice(source, snapshot, agent, repository, config, plugin, backupConfigOf(session, snapshot))
        }

        /** The config the step that made [snapshot] ran with; an empty object if the step is gone. */
        fun backupConfigOf(
            session: Session,
            snapshot: SnapshotRecord,
        ): JsonNode =
            session.find(RunStepRecord::class.java, snapshot.stepId)?.let { JSON.readTree(it.config) }
                ?: JSON.createObjectNode()

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
            return agent.also(::requireRestoring)
        }

        /** An unrevoked (409) agent whose role restores (422), in the order of agent-role Р7. */
        private fun requireRestoring(agent: Agent) {
            if (agent.revokedAt != null) throw AgentRevoked(agent.id)
            if (!AgentRole.of(agent.role).allows(Action.RESTORE)) throw AgentRoleMismatch(agent.id, Action.RESTORE)
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
