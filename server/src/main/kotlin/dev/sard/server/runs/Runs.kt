// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.runs

import dev.sard.server.persistence.PageKey
import dev.sard.server.persistence.RunRecord
import dev.sard.server.persistence.RunStepRecord
import dev.sard.server.persistence.SourceRecord
import dev.sard.server.persistence.TenantSessions
import dev.sard.server.persistence.UuidV7
import dev.sard.server.persistence.hqlWhere
import jakarta.persistence.LockModeType
import org.hibernate.Session
import org.hibernate.exception.ConstraintViolationException
import tools.jackson.databind.json.JsonMapper
import java.time.Clock
import java.time.Instant
import java.util.UUID

private const val ACTIVE_RUN_KEY = "runs_active_source_key"
private const val STEPS_OF = "from RunStepRecord where runId = :run order by ordinal"
private const val LIST_RUNS =
    "select r, s.agentId, src from RunRecord r join RunStepRecord s on s.runId = r.id and s.ordinal = 0 " +
        "join SourceRecord src on src.id = r.sourceId"
private val JSON = JsonMapper.builder().build()

/**
 * Learns that an agent has a new queued step, once its run is committed; the dispatcher
 * (agents/dispatch, S6a) sends it if the agent is online. Must not throw: the run exists already.
 */
fun interface StepsQueued {
    fun onQueued(
        tenantId: UUID,
        agentId: UUID,
    )

    companion object {
        /** Nobody listens: steps wait for the agent's next Hello. */
        val NONE = StepsQueued { _, _ -> }
    }
}

/** Which runs a list shows: all conditions together; [statuses] are alternatives; [queuedTo] is exclusive. */
data class RunFilter(
    val sourceId: UUID? = null,
    val agentId: UUID? = null,
    val statuses: Set<RunState> = emptySet(),
    val queuedFrom: Instant? = null,
    val queuedTo: Instant? = null,
) {
    /** A condition of the list and the value of its parameter; a filter that is not given has no value. */
    private class Term(
        val condition: String,
        val name: String,
        val value: Any?,
    )

    private fun terms(): List<Term> =
        listOf(
            Term("r.sourceId = :source", "source", sourceId),
            Term("s.agentId = :agent", "agent", agentId),
            Term("r.status in :statuses", "statuses", statuses.map { it.stored }.ifEmpty { null }),
            Term("r.queuedAt >= :from", "from", queuedFrom),
            Term("r.queuedAt < :to", "to", queuedTo),
        ).filter { it.value != null }

    internal fun conditions(): List<String> = terms().map { it.condition }

    internal fun bind(query: org.hibernate.query.SelectionQuery<*>) {
        for (term in terms()) {
            when (val value = term.value) {
                is List<*> -> query.setParameterList(term.name, value)
                else -> query.setParameter(term.name, value)
            }
        }
    }
}

/**
 * Starts runs of a source (S6a; S8b puts `POST /sources/{id}/runs` in front). One transaction
 * creates the run and its single backup step in `queued`, with the source's config as of now.
 * D6 is held by the partial unique index; the check before it only finds the id for the answer.
 */
class Runs(
    private val sessions: TenantSessions,
    private val clock: Clock,
    private val ids: UuidV7,
    private val queued: StepsQueued,
) {
    /** A manual run of [sourceId] in [tenantId], the tenant of the caller. */
    fun start(
        tenantId: UUID,
        sourceId: UUID,
    ): RunView {
        val run =
            try {
                sessions.inTenant(tenantId) { session ->
                    create(session, sourceId, Trigger.MANUAL, null, LockModeType.PESSIMISTIC_READ)
                }
            } catch (e: ConstraintViolationException) {
                if (e.constraintName != ACTIVE_RUN_KEY) throw e
                // The run that won the index; if it has finished meanwhile, the source is free again.
                val active = sessions.inTenant(tenantId) { session -> activeRunOf(session, sourceId) }
                throw RunActive(active ?: return start(tenantId, sourceId))
            }
        queued.onQueued(tenantId, run.agentId)
        return run
    }

    /**
     * A run of [sourceId] started by [scheduleId] inside the scheduler's transaction [session] (F3a): the same
     * checks and run as [start], refused with the same [RunsException]s. The source's row is locked exclusively,
     * so a concurrent manual start waits for this transaction and then sees its run, instead of racing on the
     * D6 index. The caller tells [StepsQueued] once its transaction commits.
     */
    internal fun startScheduled(
        session: Session,
        sourceId: UUID,
        trigger: Trigger,
        scheduleId: UUID,
    ): RunView = create(session, sourceId, trigger, scheduleId, LockModeType.PESSIMISTIC_WRITE)

    /** The run [runId] of the tenant with its steps; null if there is none (a deleted source's run is one). */
    fun get(
        tenantId: UUID,
        runId: UUID,
    ): RunView? =
        sessions.inTenant(tenantId) { session ->
            val run = session.find(RunRecord::class.java, runId) ?: return@inTenant null
            val steps =
                session
                    .createSelectionQuery(
                        STEPS_OF,
                        RunStepRecord::class.java,
                    ).setParameter("run", runId)
                    .list()
            val source = session.find(SourceRecord::class.java, run.sourceId)!!
            RunViews.of(run, steps.first().agentId, source, steps)
        }

    /**
     * Runs newest first, those matching [filter], after [after] if given, at most [limit], without their
     * steps. A run's agent is the agent of its step, not its source's current agent (S8b В16).
     */
    fun list(
        tenantId: UUID,
        filter: RunFilter,
        after: PageKey?,
        limit: Int,
    ): List<RunView> =
        sessions.inTenant(tenantId) { session ->
            val conditions = filter.conditions() + listOfNotNull(after?.let { PageKey.condition("r.queuedAt", "r.id") })
            val where = hqlWhere(conditions)
            val query =
                session.createSelectionQuery(
                    "$LIST_RUNS $where order by r.queuedAt desc, r.id desc",
                    Array<Any?>::class.java,
                )
            filter.bind(query)
            after?.bind(query)
            query.setMaxResults(limit).list().map {
                RunViews.of(it[0] as RunRecord, it[1] as UUID, it[2] as SourceRecord, emptyList())
            }
        }

    /**
     * A manual start takes a shared [sourceLock]: concurrent starts race on the index, a delete waits for them (and
     * they for it). A scheduled start takes it exclusively (see [startScheduled]).
     */
    private fun create(
        session: Session,
        sourceId: UUID,
        trigger: Trigger,
        scheduleId: UUID?,
        sourceLock: LockModeType,
    ): RunView {
        val source = liveSource(session, sourceId, sourceLock)
        AgentOffer.require(session, source.agentId, source.plugin, source.repositoryName, LockModeType.PESSIMISTIC_READ)
        activeRunOf(session, sourceId)?.let { throw RunActive(it) }
        val now = clock.instant()
        val run = RunRecord(ids.next(), sourceId, trigger.stored, RunState.QUEUED.stored, now, scheduleId)
        val step = backupStep(run, source)
        session.persist(run)
        session.persist(step)
        session.flush()
        return RunViews.of(run, source.agentId, source, listOf(step))
    }

    private fun backupStep(
        run: RunRecord,
        source: SourceRecord,
    ) = RunStepRecord(
        id = ids.next(),
        runId = run.id,
        ordinal = 0,
        agentId = source.agentId,
        sourceId = source.id,
        plugin = source.plugin,
        action = Action.BACKUP.stored,
        repositoryName = source.repositoryName,
        config = source.config,
        status = StepState.QUEUED.stored,
        queuedAt = run.queuedAt,
    )
}

/** Stored rows to views. */
internal object RunViews {
    fun of(
        run: RunRecord,
        agentId: UUID,
        source: SourceRecord,
        steps: List<RunStepRecord>,
    ) = RunView(
        id = run.id,
        sourceId = run.sourceId,
        sourceName = source.name,
        sourceDeleted = source.deletedAt != null,
        agentId = agentId,
        trigger = Trigger.of(run.trigger),
        status = RunState.of(run.status),
        message = run.message,
        queuedAt = run.queuedAt,
        startedAt = run.startedAt,
        finishedAt = run.finishedAt,
        steps = steps.map { step(it) },
    )

    fun step(record: RunStepRecord) =
        StepView(
            id = record.id,
            ordinal = record.ordinal,
            action = Action.of(record.action),
            status = StepState.of(record.status),
            agentId = record.agentId,
            sourceId = record.sourceId,
            plugin = record.plugin,
            repositoryName = record.repositoryName,
            config = record.config,
            queuedAt = record.queuedAt,
            dispatchedAt = record.dispatchedAt,
            phase = record.phase,
            bytesProcessed = record.bytesProcessed,
            bytesTotal = record.bytesTotal,
            message = record.message,
            startedAt = record.startedAt,
            finishedAt = record.finishedAt,
            filesProcessed = record.filesProcessed,
            filesTotal = record.filesTotal,
            backup = backupOf(record.output),
            runId = record.runId,
        )

    /** The backup output of a step, null for any other output or none. */
    private fun backupOf(output: String?): BackupResult? =
        output
            ?.let { JSON.readTree(it) }
            ?.takeIf { it.path("kind").asString() == Action.BACKUP.stored }
            ?.let {
                BackupResult(
                    it.path("snapshotId").asString(),
                    it.path("totalBytes").asLong(),
                    it.path("addedBytes").asLong(),
                    it.path("repositoryId").asString(),
                    it.path("partial").asBoolean(false),
                )
            }
}
