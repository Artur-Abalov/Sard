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
import tools.jackson.databind.JsonNode
import tools.jackson.databind.json.JsonMapper
import java.time.Clock
import java.time.Duration
import java.time.Instant
import java.util.UUID

/** The one requester of the open core (F4a: one administrator); names come with roles (D21). */
internal const val ADMIN = "admin"

private const val ACTIVE_RUN_KEY = "runs_active_source_key"
private const val STEPS_OF = "from RunStepRecord where runId = :run order by ordinal"
private const val FORGOTTEN =
    "select s.stepId from SnapshotRecord s where s.stepId = :step and s.forgottenAt is not null"
private const val LIST_RUNS =
    "select r, s.agentId, src, s.status, s.timeoutSeconds " +
        "from RunRecord r join RunStepRecord s on s.runId = r.id and s.ordinal = 0 " +
        "join SourceRecord src on src.id = r.sourceId"
private const val LIST_STEP_STATUS = 3
private const val LIST_STEP_TIMEOUT = 4
private val JSON = JsonMapper.builder().build()

/**
 * Whether an agent has a session now. The runs package does not know sessions: agents/dispatch implements it (J1c),
 * and where nobody does, every agent counts as online.
 */
fun interface AgentOnline {
    fun online(agentId: UUID): Boolean

    companion object {
        val ALWAYS = AgentOnline { true }
    }
}

/** How long a run waits for an offline agent unless `sard.run.agent-wait.max` says otherwise (J1c, ВJ1). */
val DEFAULT_AGENT_WAIT_MAX: Duration = Duration.ofHours(24)

/** Timeouts are set in minutes and kept in seconds, as the agent takes them. */
internal const val SECONDS_PER_MINUTE = 60L

/** The run history of REST lists backups; verifications have their own (V4). */
private const val BACKUPS_ONLY = "r.trigger <> 'verification'"

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
    val kind: RunKind? = null,
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
            Term("r.kind = :kind", "kind", kind?.stored),
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
    /** The longest a run waits for an offline agent (J1c, `sard.run.agent-wait.max`). */
    private val agentWaitMax: Duration = DEFAULT_AGENT_WAIT_MAX,
    /** Whether an agent has a session now; a queued run of an offline agent is waiting for it (J1c). */
    private val presence: AgentOnline = AgentOnline.ALWAYS,
) {
    /** A manual run of [sourceId] in [tenantId], the tenant of the caller. */
    fun start(
        tenantId: UUID,
        sourceId: UUID,
    ): RunView {
        val run =
            try {
                sessions.inTenant(tenantId) { session ->
                    create(session, sourceId, Trigger.MANUAL, null, LockModeType.PESSIMISTIC_READ, null)
                }
            } catch (e: ConstraintViolationException) {
                if (e.constraintName != ACTIVE_RUN_KEY) throw e
                // The run that won the index; if it has finished meanwhile, the source is free again.
                val active = sessions.inTenant(tenantId) { session -> activeRunOf(session, sourceId, RunKind.BACKUP) }
                throw RunActive(active ?: return start(tenantId, sourceId))
            }
        queued.onQueued(tenantId, run.agentId)
        return run
    }

    /**
     * A run of [sourceId] started by [scheduleId] inside the scheduler's transaction [session] (F3a): the same
     * checks and run as [start], refused with the same [RunsException]s. The source's row is locked exclusively,
     * so a concurrent manual start waits for this transaction and then sees its run, instead of racing on the
     * D6 index. The caller tells [StepsQueued] once its transaction commits. The run waits for an offline agent
     * until the schedule's [nextFire], at most [agentWaitMax] (J1c).
     */
    internal fun startScheduled(
        session: Session,
        sourceId: UUID,
        trigger: Trigger,
        scheduleId: UUID,
        nextFire: Instant,
    ): RunView = create(session, sourceId, trigger, scheduleId, LockModeType.PESSIMISTIC_WRITE, nextFire)

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
            val agentId = steps.first().agentId
            val view = RunViews.of(run, agentId, source, steps, agentOnline = onlineOf(run, agentId))
            view.copy(steps = view.steps.map { withRestorable(session, it) })
        }

    /** A step with a backup output learns whether the console can restore its snapshot (R2). */
    private fun withRestorable(
        session: Session,
        step: StepView,
    ): StepView {
        val backup = step.backup ?: return step
        val forgotten =
            session
                .createSelectionQuery(FORGOTTEN, UUID::class.java)
                .setParameter("step", step.id)
                .resultList
                .isNotEmpty()
        val blocked = RestoreSupport.blocked(step.plugin, backup.partial, forgotten)
        return step.copy(backup = backup.copy(restorable = blocked == null))
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
            val conditions =
                listOf(BACKUPS_ONLY) + filter.conditions() +
                    listOfNotNull(after?.let { PageKey.condition("r.queuedAt", "r.id") })
            val where = hqlWhere(conditions)
            val query =
                session.createSelectionQuery(
                    "$LIST_RUNS $where order by r.queuedAt desc, r.id desc",
                    Array<Any?>::class.java,
                )
            filter.bind(query)
            after?.bind(query)
            query.setMaxResults(limit).list().map { listed(it) }
        }

    /** A row of [LIST_RUNS]: the run, its step's agent, its source and its step's status. */
    private fun listed(row: Array<Any?>): RunView {
        val run = row[0] as RunRecord
        val agent = row[1] as UUID
        return RunViews.of(
            run,
            agent,
            row[2] as SourceRecord,
            emptyList(),
            StepState.of(row[LIST_STEP_STATUS] as String),
            onlineOf(run, agent),
            row[LIST_STEP_TIMEOUT] as Long?,
        )
    }

    /** Only a queued run waits for its agent; whether the others' agents are online is not asked (J1c). */
    private fun onlineOf(
        run: RunRecord,
        agentId: UUID,
    ): Boolean = RunState.of(run.status) != RunState.QUEUED || presence.online(agentId)

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
        nextFire: Instant?,
    ): RunView {
        val source = liveSource(session, sourceId, sourceLock)
        AgentOffer.require(
            session,
            source.agentId,
            source.plugin,
            source.repositoryName,
            Action.BACKUP,
            LockModeType.PESSIMISTIC_READ,
        )
        activeRunOf(session, sourceId, RunKind.BACKUP)?.let { throw RunActive(it) }
        val now = clock.instant()
        val requestedBy = if (trigger == Trigger.MANUAL) ADMIN else null
        val run =
            RunRecord(
                ids.next(),
                sourceId,
                trigger.stored,
                RunState.QUEUED.stored,
                now,
                scheduleId,
                requestedBy = requestedBy,
                agentWaitUntil = agentWaitUntil(now, nextFire),
            )
        val minutes = source.backupTimeoutMinutes ?: defaultsOf(session, source).backupMinutes
        val step = backupStep(run, source, minutes * SECONDS_PER_MINUTE)
        session.persist(run)
        session.persist(step)
        session.flush()
        return RunViews.of(run, source.agentId, source, listOf(step))
    }

    /** A scheduled run waits until the schedule fires again, the others and the far fires, the longest wait. */
    private fun agentWaitUntil(
        now: Instant,
        nextFire: Instant?,
    ): Instant = minOf(now + agentWaitMax, nextFire ?: Instant.MAX)

    private fun defaultsOf(
        session: Session,
        source: SourceRecord,
    ) = TimeoutSettings.read(session, checkNotNull(source.tenantId))

    private fun backupStep(
        run: RunRecord,
        source: SourceRecord,
        timeoutSeconds: Long,
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
        timeoutSeconds = timeoutSeconds,
    )
}

/** Stored rows to views. */
internal object RunViews {
    fun of(
        run: RunRecord,
        agentId: UUID,
        source: SourceRecord,
        steps: List<RunStepRecord>,
        stepStatus: StepState? = null,
        agentOnline: Boolean = true,
        stepTimeoutSeconds: Long? = null,
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
        kind = RunKind.of(run.kind),
        requestedBy = run.requestedBy,
        restore = run.restore?.let(::restoreInfo),
        cancellation = cancellation(run),
        stepStatus = stepStatus,
        agentWaitUntil = run.agentWaitUntil.takeIf { RunState.of(run.status) == RunState.QUEUED },
        agentUnavailableSince = run.agentUnavailableSince,
        agentOnline = agentOnline,
        stepTimeoutSeconds = stepTimeoutSeconds,
    )

    private fun cancellation(run: RunRecord): Cancellation? {
        val at = run.cancelRequestedAt ?: return null
        return Cancellation(at, checkNotNull(run.cancelRequestedBy), CancelResult.of(checkNotNull(run.cancelResult)))
    }

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
            restore = restoreOf(record.output),
            snapshotId = record.snapshotId,
            verifyTask = VerifyTask.of(record.verifyTask),
            lateStatus = record.lateStatus?.let(StepState::of),
            timeoutSeconds = record.timeoutSeconds,
            timedOutBy = record.timedOutBy?.let(TimedOutBy::of),
        )

    fun restoreInfo(json: String): RestoreInfo {
        val node = JSON.readTree(json)
        return RestoreInfo(
            node.path("snapshotId").asString(),
            Instant.parse(node.path("snapshotCreatedAt").asString()),
            node.path("repositoryName").asString(),
            node.path("config").toString(),
            node.path("target").asString(),
        )
    }

    /**
     * The restore output of a step, null for any other output or none. What a kind of restore does not report is
     * null: a database restore has no file counts, a restore of files no tables; a failed one has only its leftover.
     */
    private fun restoreOf(output: String?): RestoreResult? {
        val node = output?.let { JSON.readTree(it) }?.takeIf { it.path("kind").asString() == Action.RESTORE.stored }
        return node?.let(::restoreResult)
    }

    private fun restoreResult(node: JsonNode): RestoreResult? {
        val target = node.text("target")
        val database = node.text("database")
        val leftover = node.text("leftover")
        return when {
            database != null -> {
                val tables = node.path("tablesRestored").asLong()
                val roles = node.path("rolesRestored").asLong()
                RestoreResult(target, null, null, database, tables, roles, null, leftover)
            }

            target != null -> {
                val files = node.path("filesRestored").asLong()
                val bytes = node.path("bytesRestored").asLong()
                RestoreResult(target, files, bytes, null, null, null, node.path("ownersRestored").asBoolean(), leftover)
            }

            leftover != null -> {
                RestoreResult(null, null, null, null, null, null, null, leftover)
            }

            else -> {
                null
            }
        }
    }

    /** A text field of the output, null when absent or empty. */
    private fun JsonNode.text(name: String): String? = path(name).asString().ifEmpty { null }

    /** The backup output of a step, null for any other output or none. */
    private fun backupOf(output: String?): BackupResult? =
        output
            ?.let { JSON.readTree(it) }
            ?.takeIf { it.path("kind").asString() == Action.BACKUP.stored }
            ?.let(::backupResult)

    private fun backupResult(node: JsonNode): BackupResult =
        BackupResult(
            node.path("snapshotId").asString(),
            node.path("totalBytes").asLong(),
            node.path("addedBytes").asLong(),
            node.path("repositoryId").asString(),
            node.path("partial").asBoolean(false),
            totalFiles = node.path("totalFiles").takeIf(JsonNode::isNumber)?.asLong(),
            fingerprint = node.path("fingerprint").takeIf(JsonNode::isObject)?.let(FingerprintSummary::of),
            warnings = warningsOf(node.path("warnings")),
        )

    private fun warningsOf(node: JsonNode): List<String> = (0 until node.size()).map { node.get(it).asString() }
}
