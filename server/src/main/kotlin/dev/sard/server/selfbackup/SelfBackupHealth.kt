// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.selfbackup

import dev.sard.server.persistence.RunRecord
import dev.sard.server.persistence.SourceRecord
import dev.sard.server.runs.RunState
import org.hibernate.Session
import java.time.Instant
import java.util.UUID

private const val LAST_AT =
    "select max(r.finishedAt) from RunRecord r where r.sourceId = :source and r.kind = 'backup' and r.status = :status"
private const val LAST_RUN =
    "from RunRecord r where r.sourceId = :source and r.kind = 'backup' and r.finishedAt is not null order by r.finishedAt desc, r.id desc"
private const val SUCCEEDED_SINCE =
    "select count(r) from RunRecord r where r.sourceId = :source and r.kind = 'backup' and r.status = :status and r.queuedAt >= :since"
private const val SCHEDULE_ON = "select s.enabled from ScheduleRecord s where s.sourceId = :source"
private const val OVERDUE_SINCE =
    "select e.deadline from OverdueEpisodeRecord e where e.sourceId = :source and e.closedAt is null"

/** How the self-backup is doing, the worst of its warnings (F6); the console words it, the server decides it. */
enum class SelfBackupHealth { NOT_CONFIGURED, OK, AGENT_REPLACED, OVERDUE, FAILING, SCHEDULE_DISABLED }

/**
 * What the console warns about on every page (F6, D8), worst first: the system sources are on an agent that is no
 * longer the built-in one, a source is overdue, its last run failed, its schedule is off, the repository is local,
 * the key copy of the current binding is not confirmed although the onboarding is over (F4b, OQ-206).
 */
enum class SelfBackupWarning { AGENT_REPLACED, OVERDUE, FAILING, SCHEDULE_DISABLED, LOCAL_STORAGE, KEYS_UNCONFIRMED }

/** A source's last finished run. */
data class LastRun(
    val id: UUID,
    val status: RunState,
    val finishedAt: Instant,
)

/** What the health is judged from, for one system source. */
internal data class SourceState(
    val agentId: UUID,
    val lastSuccessAt: Instant?,
    val lastFailureAt: Instant?,
    val lastRun: LastRun?,
    val scheduleOn: Boolean,
    /** The deadline of its open overdue episode; null when it is not overdue. */
    val overdueSince: Instant?,
    /** A run queued at or after the binding ended succeeded: the source's part of the first backup (F4b). */
    val succeededSinceBound: Boolean = false,
)

/** Reads a system source's state in the tenant's session. */
internal object SourceStates {
    fun of(
        session: Session,
        source: SourceRecord,
        boundAt: Instant,
    ): SourceState =
        SourceState(
            agentId = source.agentId,
            lastSuccessAt = lastAt(session, source.id, RunState.SUCCEEDED),
            lastFailureAt = lastAt(session, source.id, RunState.FAILED),
            lastRun = lastRun(session, source.id),
            scheduleOn = scheduleOn(session, source.id),
            overdueSince = overdueSince(session, source.id),
            succeededSinceBound = succeededSince(session, source.id, boundAt),
        )

    private fun succeededSince(
        session: Session,
        source: UUID,
        since: Instant,
    ): Boolean =
        session
            .createSelectionQuery(SUCCEEDED_SINCE, Long::class.javaObjectType)
            .setParameter("source", source)
            .setParameter("status", RunState.SUCCEEDED.stored)
            .setParameter("since", since)
            .singleResult > 0

    private fun lastAt(
        session: Session,
        source: UUID,
        status: RunState,
    ): Instant? =
        session
            .createSelectionQuery(LAST_AT, Instant::class.java)
            .setParameter("source", source)
            .setParameter("status", status.stored)
            .singleResult

    private fun lastRun(
        session: Session,
        source: UUID,
    ): LastRun? {
        val run =
            session
                .createSelectionQuery(LAST_RUN, RunRecord::class.java)
                .setParameter("source", source)
                .setMaxResults(1)
                .uniqueResult() ?: return null
        return LastRun(run.id, RunState.of(run.status), checkNotNull(run.finishedAt))
    }

    private fun scheduleOn(
        session: Session,
        source: UUID,
    ): Boolean =
        session
            .createSelectionQuery(SCHEDULE_ON, Boolean::class.javaObjectType)
            .setParameter("source", source)
            .uniqueResult() == true

    private fun overdueSince(
        session: Session,
        source: UUID,
    ): Instant? =
        session
            .createSelectionQuery(OVERDUE_SINCE, Instant::class.java)
            .setParameter("source", source)
            .uniqueResult()
}

/** The rules of the self-backup's health and warnings. */
internal object HealthRules {
    /** The warnings a source can raise, worst first; [SelfBackupWarning.LOCAL_STORAGE] is the repository's. */
    private val sourceRules: List<Pair<SelfBackupWarning, (SourceState, UUID?) -> Boolean>> =
        listOf(
            SelfBackupWarning.AGENT_REPLACED to { state, agent -> state.agentId != agent },
            SelfBackupWarning.OVERDUE to { state, _ -> state.overdueSince != null },
            SelfBackupWarning.FAILING to { state, _ -> state.lastRun?.status == RunState.FAILED },
            SelfBackupWarning.SCHEDULE_DISABLED to { state, _ -> !state.scheduleOn },
        )

    fun warnings(
        states: List<SourceState>,
        builtinAgent: UUID?,
        local: Boolean,
        keysUnconfirmed: Boolean = false,
    ): List<SelfBackupWarning> {
        val raised = sourceRules.filter { (_, holds) -> states.any { holds(it, builtinAgent) } }.map { it.first }
        return raised +
            listOfNotNull(
                SelfBackupWarning.LOCAL_STORAGE.takeIf { local },
                SelfBackupWarning.KEYS_UNCONFIRMED.takeIf { keysUnconfirmed },
            )
    }

    /** The first backup of a binding is done when every system source backed up after it. */
    fun firstBackupDone(states: List<SourceState>): Boolean = states.all { it.succeededSinceBound }

    /** The worst warning about the backup itself; local storage alone leaves it OK. */
    fun health(warnings: List<SelfBackupWarning>): SelfBackupHealth =
        when (warnings.firstOrNull()) {
            SelfBackupWarning.AGENT_REPLACED -> SelfBackupHealth.AGENT_REPLACED
            SelfBackupWarning.OVERDUE -> SelfBackupHealth.OVERDUE
            SelfBackupWarning.FAILING -> SelfBackupHealth.FAILING
            SelfBackupWarning.SCHEDULE_DISABLED -> SelfBackupHealth.SCHEDULE_DISABLED
            SelfBackupWarning.LOCAL_STORAGE, SelfBackupWarning.KEYS_UNCONFIRMED, null -> SelfBackupHealth.OK
        }

    /** When the self-backup went overdue: the earliest deadline of its open episodes. */
    fun overdueSince(states: List<SourceState>): Instant? = states.mapNotNull { it.overdueSince }.minOrNull()
}
