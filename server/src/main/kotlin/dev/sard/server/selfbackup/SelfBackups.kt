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
import dev.sard.server.runs.Action
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
private const val BUILTIN_REPOSITORIES = "from AgentRepositoryRecord where agentId = :agent"
private const val SYSTEM_SOURCES =
    "from SourceRecord where systemRole is not null and deletedAt is null order by systemRole"

/** Constraints two first bindings of one tenant race on: the loser binds again over the winner's rows. */
private val RACE_KEYS = setOf("sources_tenant_id_system_role_key", "self_backups_tenant_id_key")

/** The role of a source read through [SYSTEM_SOURCES]: never null there. */
private fun roleOf(source: SourceRecord): SystemRole = SystemRole.of(checkNotNull(source.systemRole))

private fun systemSourceOf(
    source: SourceRecord,
    state: SourceState,
): SystemSource {
    val role = roleOf(source)
    return SystemSource(
        role,
        source.id,
        source.agentId,
        source.repositoryName,
        state.lastSuccessAt,
        state.lastFailureAt,
        state.lastRun,
    )
}

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

/** Whether the onboarding is over: only then an unconfirmed key copy is a warning, not a step of the wizard. */
fun interface OnboardingCompletion {
    fun complete(): Boolean
}

/** The key copy is confirmed for [repositoryName]; [recorded] is false when the binding had it already. */
data class KeysConfirmed(
    val repositoryName: String,
    val repositoryId: String?,
    val recorded: Boolean,
)

/** One of the two system sources. */
data class SystemSource(
    val role: SystemRole,
    val sourceId: UUID,
    val agentId: UUID,
    val repositoryName: String,
    val lastSuccessAt: Instant? = null,
    val lastFailureAt: Instant? = null,
    val lastRun: LastRun? = null,
)

/** The self-backup of a tenant; [agentId] is its live built-in agent, whether bound or not. */
data class SelfBackupView(
    val configured: Boolean,
    val agentId: UUID?,
    val repository: RepositoryState?,
    val boundAt: Instant?,
    val sources: List<SystemSource>,
    val health: SelfBackupHealth = SelfBackupHealth.NOT_CONFIGURED,
    /** Worst first; empty when nothing is to be warned about. */
    val warnings: List<SelfBackupWarning> = emptyList(),
    /** The earliest deadline of the system sources' open overdue episodes. */
    val overdueSince: Instant? = null,
    /** The repositories of the live built-in agent's last Register, by name, whether bound or not (F4b). */
    val repositories: List<RepositoryState> = emptyList(),
    /** What the system sources back up (F4b, Р26). */
    val plan: List<PlanItem> = emptyList(),
    /** Both system sources backed up successfully after [boundAt] (Р20). */
    val firstBackupDone: Boolean = false,
    /** The key copy confirmed for the current binding; null when not, or confirmed before [boundAt] (OQ-206). */
    val keysConfirmedAt: Instant? = null,
    /** What prints the repository password on the server's machine; null until bound (Р27). */
    val passwordRevealCommand: String? = null,
    /** What issues a recovery key for the bound repository on the server's machine; null until bound (V3, КС1). */
    val recoveryKeyCommand: String? = null,
    /** The storage address the wizard built the command for, without credentials (OQ-203). */
    val location: String? = null,
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
    /** The onboarding is over: only then an unconfirmed key copy is a warning, not a step of the wizard. */
    private val onboardingComplete: () -> Boolean = { false },
) {
    /**
     * Binds the self-backup to [repositoryName] of the live built-in agent: creates the system sources with their
     * schedule, or moves them to this agent and repository. Binding again unchanged changes nothing. Throws
     * [LocationInvalid], [SelfAgentMissing], UnknownRepository, [RepositoryNotInitialized],
     * [LocalStorageUnconfirmed] or, when the agent lacks a plugin or the database password's secret, UnknownPlugin
     * or InvalidConfig.
     */
    fun bind(
        tenantId: UUID,
        repositoryName: String,
        confirmLocalStorage: Boolean,
        location: String? = null,
    ): SelfBackupView {
        if (location != null && !isSafeLocation(location)) throw LocationInvalid()
        // Asked before the session: the pool may hold one connection, and a request needs one at a time.
        val complete = onboardingComplete()
        while (true) {
            try {
                return sessions.inTenant(tenantId) { session ->
                    bindIn(session, repositoryName, confirmLocalStorage, location, complete)
                }
            } catch (e: ConstraintViolationException) {
                if (e.constraintName !in RACE_KEYS) throw e
            }
        }
    }

    fun get(tenantId: UUID): SelfBackupView {
        val complete = onboardingComplete()
        return sessions.inTenant(tenantId) { session -> viewIn(session, complete) }
    }

    /**
     * The owner confirms the copy of the repository password and the storage access made outside the server
     * (F4b). Throws [SelfBackupNotConfigured] before a repository is bound and [FirstBackupPending] before both
     * system sources backed up after the binding; confirming again changes nothing.
     */
    fun confirmKeys(
        tenantId: UUID,
        now: Instant,
    ): KeysConfirmed {
        val complete = onboardingComplete()
        return sessions.inTenant(tenantId) { session -> confirmIn(session, now, complete) }
    }

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

    private fun confirmIn(
        session: Session,
        now: Instant,
        complete: Boolean,
    ): KeysConfirmed {
        val record = bindingOf(session, LockModeType.PESSIMISTIC_WRITE)
        val view = viewIn(session, complete)
        val repository = view.repository
        if (record == null || repository == null) throw SelfBackupNotConfigured()
        if (!view.firstBackupDone) throw FirstBackupPending()
        val recorded = view.keysConfirmedAt == null
        if (recorded) record.keysConfirmedAt = now
        return KeysConfirmed(repository.name, repository.repositoryId, recorded)
    }

    private fun bindIn(
        session: Session,
        repositoryName: String,
        confirmLocalStorage: Boolean,
        location: String?,
        complete: Boolean,
    ): SelfBackupView {
        val agent = liveBuiltin(session) ?: throw SelfAgentMissing()
        val repository = repositoryOf(session, agent.id, repositoryName) ?: throw UnknownRepository(repositoryName)
        repository.requireBindable(confirmLocalStorage)
        val existing = systemSources(session, LockModeType.PESSIMISTIC_WRITE).associateBy(::roleOf)
        val now = clock.instant()
        val moved =
            plan.sources().map { planned ->
                AgentOffer
                    .require(session, agent.id, planned.plugin, repositoryName, Action.BACKUP, requireListed = true)
                    .requireConfig(mapper.writeValueAsString(planned.config))
                keep(session, existing[planned.role], planned, agent.id, repositoryName, now)
            }
        record(session, moved.any { it }, now, location)
        session.flush()
        return viewIn(session, complete)
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
        val wanted = listOf(agentId, repositoryName, planned.plugin, planned.name)
        val same =
            listOf(source.agentId, source.repositoryName, source.plugin, source.name) == wanted &&
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

    /**
     * Creates the binding, or marks it bound again now when the sources [moved], with the new [location] (none
     * without one). Unmoved, the binding keeps its address unless a new one comes.
     */
    private fun record(
        session: Session,
        moved: Boolean,
        now: Instant,
        location: String?,
    ) {
        val record = bindingOf(session, LockModeType.PESSIMISTIC_WRITE)
        if (record == null) {
            session.persist(SelfBackupRecord(ids.next(), now, now).also { it.location = location })
        } else if (moved) {
            record.boundAt = now
            record.updatedAt = now
            record.location = location
        } else if (location != null && location != record.location) {
            record.location = location
            record.updatedAt = now
        }
    }

    private fun viewIn(
        session: Session,
        complete: Boolean,
    ): SelfBackupView {
        val agentId = liveBuiltin(session)?.id
        val record = bindingOf(session, LockModeType.NONE)
        val sources = systemSources(session, LockModeType.NONE)
        val repositories = repositoriesOf(session, agentId)
        if (record == null || sources.size != SystemRole.entries.size) {
            return SelfBackupView(
                configured = false,
                agentId = agentId,
                repository = null,
                boundAt = null,
                sources = emptyList(),
                repositories = repositories,
                plan = plan.items(),
            )
        }
        val states = sources.map { SourceStates.of(session, it, record.boundAt) }
        val repository = repositoryState(session, sources.first())
        val view = viewOf(record, sources, states, agentId, repository, complete)
        return view.copy(repositories = repositories, plan = plan.items())
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

/** What the live built-in agent's last Register lists, by name; none without such an agent. */
private fun repositoriesOf(
    session: Session,
    agentId: UUID?,
): List<RepositoryState> {
    if (agentId == null) return emptyList()
    return session
        .createSelectionQuery(BUILTIN_REPOSITORIES, AgentRepositoryRecord::class.java)
        .setParameter("agent", agentId)
        .list()
        .sortedBy { it.name }
        .map { RepositoryState(it.name, it.backend, it.repositoryId, it.backend == LOCAL_BACKEND) }
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

/** The self-backup that is configured: its sources with their states, the warnings and what the wizard needs. */
private fun viewOf(
    record: SelfBackupRecord,
    sources: List<SourceRecord>,
    states: List<SourceState>,
    agentId: UUID?,
    repository: RepositoryState,
    onboardingComplete: Boolean,
): SelfBackupView {
    val keysConfirmedAt = record.keysConfirmedAt?.takeIf { !it.isBefore(record.boundAt) }
    val keysUnconfirmed = keysConfirmedAt == null && onboardingComplete
    val warnings = HealthRules.warnings(states, agentId, repository.local, keysUnconfirmed)
    return SelfBackupView(
        configured = true,
        agentId = agentId,
        repository = repository,
        boundAt = record.boundAt,
        sources = sources.zip(states, ::systemSourceOf),
        health = HealthRules.health(warnings),
        warnings = warnings,
        overdueSince = HealthRules.overdueSince(states),
        firstBackupDone = HealthRules.firstBackupDone(states),
        keysConfirmedAt = keysConfirmedAt,
        passwordRevealCommand = passwordRevealCommand(repository.name),
        recoveryKeyCommand = recoveryKeyCommand(repository.name),
        location = record.location,
    )
}
