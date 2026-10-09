// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.selfbackup

import dev.sard.server.persistence.Agent
import dev.sard.server.persistence.AgentOwnedKey
import dev.sard.server.persistence.AgentRepositoryRecord
import dev.sard.server.persistence.SelfBackupRecord
import dev.sard.server.persistence.SourceRecord
import dev.sard.server.persistence.TenantSessions
import dev.sard.server.persistence.UuidV7
import dev.sard.server.runs.AgentOffer
import dev.sard.server.runs.RunActive
import dev.sard.server.runs.Runs
import dev.sard.server.runs.SystemRole
import dev.sard.server.runs.UnknownRepository
import dev.sard.server.scheduler.ScheduleDraft
import dev.sard.server.scheduler.Schedules
import jakarta.persistence.LockModeType
import org.hibernate.Session
import org.hibernate.exception.ConstraintViolationException
import tools.jackson.databind.ObjectMapper
import java.time.Clock
import java.time.Instant
import java.util.UUID

private const val LOCAL_BACKEND = "local"
private const val LIVE_BUILTIN = "from Agent where builtin = true and revokedAt is null"
private const val BINDING = "from SelfBackupRecord"
private const val SYSTEM_SOURCES =
    "from SourceRecord where systemRole is not null and deletedAt is null order by systemRole"

/** Constraints two first bindings of one tenant race on: the loser binds again over the winner's rows. */
private val RACE_KEYS = setOf("sources_tenant_id_system_role_key", "self_backups_tenant_id_key")

/** The role of a source read through [SYSTEM_SOURCES]: never null there. */
private fun roleOf(source: SourceRecord): SystemRole = SystemRole.of(checkNotNull(source.systemRole))

/** Refuses a repository restic has not initialised yet, and a local one without the confirmation (D10). */
private fun AgentRepositoryRecord.requireBindable(confirmLocalStorage: Boolean) {
    if (repositoryId == null) throw RepositoryNotInitialized(name)
    if (backend == LOCAL_BACKEND && !confirmLocalStorage) throw LocalStorageUnconfirmed(name)
}

/** The repository the system sources back up to, as the built-in agent's last Register describes it. */
data class RepositoryState(
    val name: String,
    val backend: String,
    val repositoryId: String?,
    /** A local path on the server's machine (D10): bound only with a confirmation, warned about always. */
    val local: Boolean,
)

/** One of the two system sources. */
data class SystemSource(
    val role: SystemRole,
    val sourceId: UUID,
    val agentId: UUID,
    val repositoryName: String,
)

/** The self-backup of a tenant; [agentId] is its live built-in agent, whether bound or not. */
data class SelfBackupView(
    val configured: Boolean,
    val agentId: UUID?,
    val repository: RepositoryState?,
    val boundAt: Instant?,
    val sources: List<SystemSource>,
)

/** A run of one system source asked for by "back up now"; [started] is false for a run already active (D6). */
data class SelfBackupRun(
    val role: SystemRole,
    val sourceId: UUID,
    val runId: UUID,
    val started: Boolean,
)

/**
 * The self-backup (F6): the built-in agent backs up the server's database and its keys with the configuration to a
 * repository named by the administrator. The server only learns the repository's name, backend and id from Register;
 * credentials and the repository password stay on the agent (ADR 0008).
 */
class SelfBackups internal constructor(
    private val sessions: TenantSessions,
    private val clock: Clock,
    private val ids: UuidV7,
    private val plan: SelfBackupPlan,
    private val schedule: ScheduleDraft,
    private val schedules: Schedules,
    private val runs: Runs,
    private val mapper: ObjectMapper,
) {
    /**
     * Binds the self-backup to [repositoryName] of the live built-in agent: creates the system sources with their
     * schedule, or moves them to this agent and repository. Binding again unchanged changes nothing. Throws
     * [SelfAgentMissing], UnknownRepository, [RepositoryNotInitialized], [LocalStorageUnconfirmed] or, when the
     * agent lacks a plugin or the database password's secret, UnknownPlugin or InvalidConfig.
     */
    fun bind(
        tenantId: UUID,
        repositoryName: String,
        confirmLocalStorage: Boolean,
    ): SelfBackupView =
        try {
            sessions.inTenant(tenantId) { session -> bindIn(session, repositoryName, confirmLocalStorage) }
        } catch (e: ConstraintViolationException) {
            if (e.constraintName !in RACE_KEYS) throw e
            bind(tenantId, repositoryName, confirmLocalStorage)
        }

    fun get(tenantId: UUID): SelfBackupView = sessions.inTenant(tenantId) { session -> viewIn(session) }

    /**
     * Starts a manual run of each system source, the database first; a source with an active run answers with that
     * run. Throws [SelfBackupNotConfigured], or what Runs.start refuses for a revoked agent or a missing plugin or
     * repository.
     */
    fun runNow(tenantId: UUID): List<SelfBackupRun> {
        val view = get(tenantId)
        if (!view.configured) throw SelfBackupNotConfigured()
        return view.sources.map { source ->
            try {
                SelfBackupRun(source.role, source.sourceId, runs.start(tenantId, source.sourceId).id, started = true)
            } catch (e: RunActive) {
                SelfBackupRun(source.role, source.sourceId, e.activeRunId, started = false)
            }
        }
    }

    private fun bindIn(
        session: Session,
        repositoryName: String,
        confirmLocalStorage: Boolean,
    ): SelfBackupView {
        val agent = liveBuiltin(session) ?: throw SelfAgentMissing()
        val repository = repositoryOf(session, agent.id, repositoryName) ?: throw UnknownRepository(repositoryName)
        repository.requireBindable(confirmLocalStorage)
        val existing = systemSources(session, LockModeType.PESSIMISTIC_WRITE).associateBy(::roleOf)
        val now = clock.instant()
        val moved =
            plan.sources().map { planned ->
                AgentOffer
                    .require(session, agent.id, planned.plugin, repositoryName, requireBackup = true)
                    .requireConfig(mapper.writeValueAsString(planned.config))
                keep(session, existing[planned.role], planned, agent.id, repositoryName, now)
            }
        record(session, moved.any { it }, repository.backend == LOCAL_BACKEND, now)
        session.flush()
        return viewIn(session)
    }

    /** Creates the source of [planned], or brings the existing one to it; true if anything changed. */
    private fun keep(
        session: Session,
        source: SourceRecord?,
        planned: PlannedSource,
        agentId: UUID,
        repositoryName: String,
        now: Instant,
    ): Boolean {
        if (source == null) {
            create(session, planned, agentId, repositoryName, now)
            return true
        }
        return move(source, planned, agentId, repositoryName, now)
    }

    private fun create(
        session: Session,
        planned: PlannedSource,
        agentId: UUID,
        repositoryName: String,
        now: Instant,
    ) {
        val config = mapper.writeValueAsString(planned.config)
        val created = SourceRecord(ids.next(), agentId, planned.name, planned.plugin, config, repositoryName, now, now)
        created.systemRole = planned.role.stored
        session.persist(created)
        session.flush()
        schedules.setIn(session, created.id, schedule)
    }

    /** Brings [source] to [planned] on [agentId] and [repositoryName]; false when it already is. */
    private fun move(
        source: SourceRecord,
        planned: PlannedSource,
        agentId: UUID,
        repositoryName: String,
        now: Instant,
    ): Boolean {
        val same =
            source.agentId == agentId &&
                source.repositoryName == repositoryName &&
                source.plugin == planned.plugin &&
                source.name == planned.name &&
                mapper.readTree(source.config) == mapper.valueToTree(planned.config)
        if (!same) {
            source.agentId = agentId
            source.repositoryName = repositoryName
            source.plugin = planned.plugin
            source.name = planned.name
            source.config = mapper.writeValueAsString(planned.config)
            source.updatedAt = now
        }
        return !same
    }

    private fun record(
        session: Session,
        moved: Boolean,
        local: Boolean,
        now: Instant,
    ) {
        val record = bindingOf(session, LockModeType.PESSIMISTIC_WRITE)
        if (record == null) {
            session.persist(SelfBackupRecord(ids.next(), local, now, now))
            return
        }
        if (moved) record.boundAt = now
        if (moved || record.localStorageConfirmed != local) {
            record.localStorageConfirmed = local
            record.updatedAt = now
        }
    }

    private fun viewIn(session: Session): SelfBackupView {
        val agent = liveBuiltin(session)
        val record = bindingOf(session, LockModeType.NONE)
        val sources = systemSources(session, LockModeType.NONE)
        val bound = record?.takeIf { sources.size == SystemRole.entries.size }
        val first = sources.firstOrNull()?.takeIf { bound != null }
        return SelfBackupView(
            configured = bound != null,
            agentId = agent?.id,
            repository = first?.let { repositoryState(session, it) },
            boundAt = bound?.boundAt,
            sources =
                if (bound == null) {
                    emptyList()
                } else {
                    sources.map { SystemSource(roleOf(it), it.id, it.agentId, it.repositoryName) }
                },
        )
    }

    /** The repository of the system sources; the name alone once the agent's Register no longer lists it. */
    private fun repositoryState(
        session: Session,
        source: SourceRecord,
    ): RepositoryState {
        val announced = repositoryOf(session, source.agentId, source.repositoryName)
        return RepositoryState(
            source.repositoryName,
            announced?.backend.orEmpty(),
            announced?.repositoryId,
            announced?.backend == LOCAL_BACKEND,
        )
    }
}

/** The tenant's live built-in agent; at most one (index agents_one_live_builtin). */
private fun liveBuiltin(session: Session): Agent? {
    val query = session.createSelectionQuery(LIVE_BUILTIN, Agent::class.java)
    return query.uniqueResult()
}

private fun repositoryOf(
    session: Session,
    agentId: UUID,
    name: String,
): AgentRepositoryRecord? = session.find(AgentRepositoryRecord::class.java, AgentOwnedKey(agentId, name))

/** The tenant's binding (one at most, self_backups_tenant_id_key); the session's tenant filters it. */
private fun bindingOf(
    session: Session,
    lock: LockModeType,
): SelfBackupRecord? =
    session
        .createSelectionQuery(BINDING, SelfBackupRecord::class.java)
        .setLockMode(lock)
        .uniqueResult()

/** The live system sources, database before keys (their stored roles sort that way). */
private fun systemSources(
    session: Session,
    lock: LockModeType,
): List<SourceRecord> =
    session
        .createSelectionQuery(SYSTEM_SOURCES, SourceRecord::class.java)
        .setLockMode(lock)
        .list()
