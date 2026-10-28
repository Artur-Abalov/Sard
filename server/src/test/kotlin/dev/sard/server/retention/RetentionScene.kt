// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.retention

import dev.sard.server.TestAdmin
import dev.sard.server.persistence.StepLogPartitions
import dev.sard.server.runs.RUNS_NOW
import dev.sard.server.runs.RunsTenant
import org.springframework.jdbc.core.JdbcTemplate
import java.sql.Timestamp
import java.time.Duration
import java.time.Instant
import java.time.LocalDate
import java.util.UUID

/** The moment of the pass in the specification (docs/specs/server/server-retention.feature, general conventions). */
internal val T0: Instant = Instant.parse("2027-06-15T12:00:00Z")

/** The boundary of a term: a row marked strictly before it is old, a row exactly on it stays. */
internal fun boundary(
    term: Duration,
    now: Instant = T0,
): Instant = now.minus(term)

internal val RUNS_TERM: Duration = Duration.ofDays(180)
internal val DELIVERIES_TERM: Duration = Duration.ofDays(30)

/** One second before the boundary of [term]. */
internal fun old(
    term: Duration,
    now: Instant = T0,
): Instant = boundary(term, now).minusSeconds(1)

private val ACTIVE = setOf("queued", "dispatched", "running")

/** A run with the ids of its steps. */
internal data class SceneRun(
    val id: UUID,
    val steps: List<UUID>,
)

/**
 * Rows of one tenant written straight into the tables, with the marks the test chooses: the retention rules are about
 * those marks and about who refers to whom, so nothing here goes through the services that would stamp the time.
 */
internal class SceneTenant(
    private val jdbc: JdbcTemplate,
    existing: Pair<UUID, UUID>? = null,
) {
    private val owner = if (existing == null) RunsTenant(jdbc).create() else null
    val id: UUID = existing?.first ?: owner!!.id
    val agentId: UUID = existing?.second ?: owner!!.agentId

    fun source(
        name: String = "src-${UUID.randomUUID()}",
        deleted: Boolean = false,
    ): UUID {
        val source = UUID.randomUUID()
        jdbc.update(
            """
            insert into sources (id, tenant_id, agent_id, name, plugin, config, repository_name, created_at, updated_at,
                                 deleted_at)
            values (?, ?, ?, ?, 'postgresql', '{}'::jsonb, 'main', ?, ?, ?)
            """.trimIndent(),
            source,
            id,
            agentId,
            name,
            stamp(T0.minus(Duration.ofDays(900))),
            stamp(T0.minus(Duration.ofDays(900))),
            if (deleted) stamp(T0.minus(Duration.ofDays(800))) else null,
        )
        return source
    }

    /**
     * [count] runs of [source] marked [at] (queued, started and finished alike; an active run has no finish), each
     * with [steps] steps in the run's final state. [kind] restore and trigger verification make runs of those kinds.
     */
    fun runs(
        source: UUID,
        count: Int,
        at: Instant,
        status: String = "failed",
        trigger: String = "manual",
        kind: String = "backup",
        steps: Int = 1,
    ): List<SceneRun> {
        val marker = UUID.randomUUID().toString()
        val finished = if (status in ACTIVE) null else stamp(at)
        jdbc.update(
            """
            insert into runs (id, tenant_id, source_id, trigger, status, queued_at, started_at, finished_at, kind,
                              restore, message)
            select gen_random_uuid(), ?, ?, ?, ?, ?::timestamptz, ?::timestamptz, ?::timestamptz, ?,
                   case when ? = 'restore' then '{}'::jsonb end, ?
            from generate_series(1, ?)
            """.trimIndent(),
            id,
            source,
            trigger,
            status,
            stamp(at),
            stamp(at),
            finished,
            kind,
            kind,
            marker,
            count,
        )
        val action =
            when {
                trigger == "verification" -> "verify"
                kind == "restore" -> "restore"
                else -> "backup"
            }
        for (ordinal in 0 until steps) {
            jdbc.update(
                """
                insert into run_steps (id, tenant_id, run_id, ordinal, agent_id, source_id, plugin, action,
                                       repository_name, config, status, queued_at, dispatched_at, finished_at)
                select gen_random_uuid(), tenant_id, id, ?, ?, source_id, 'postgresql', ?, 'main', '{}'::jsonb, status,
                       queued_at, case when status = 'queued' then null else queued_at end, finished_at
                from runs where tenant_id = ? and message = ?
                """.trimIndent(),
                ordinal,
                agentId,
                action,
                id,
                marker,
            )
        }
        return jdbc
            .queryForList("select id from runs where tenant_id = ? and message = ?", UUID::class.java, id, marker)
            .filterNotNull()
            .map { SceneRun(it, stepsOf(it)) }
    }

    fun run(
        source: UUID,
        at: Instant,
        status: String = "failed",
        trigger: String = "manual",
        kind: String = "backup",
        steps: Int = 1,
    ): SceneRun = runs(source, 1, at, status, trigger, kind, steps).single()

    /** A snapshot of a long finished backup of [source], which nothing but the snapshot keeps. */
    fun oldSnapshot(source: UUID): UUID {
        val backup = run(source, T0.minus(Duration.ofDays(900)), status = "succeeded")
        return snapshot(backup.steps.single(), source)
    }

    fun stepsOf(run: UUID): List<UUID> =
        jdbc
            .queryForList(
                "select id from run_steps where tenant_id = ? and run_id = ? order by ordinal",
                UUID::class.java,
                id,
                run,
            ).filterNotNull()

    /** A restic snapshot recorded for [step]. */
    fun snapshot(
        step: UUID,
        source: UUID,
        forgotten: Boolean = false,
    ): UUID {
        val snapshot = UUID.randomUUID()
        jdbc.update(
            """
            insert into snapshots (id, tenant_id, source_id, step_id, agent_id, repository_name, repository_id,
                                   snapshot_id, total_bytes, added_bytes, created_at, forgotten_at)
            values (?, ?, ?, ?, ?, 'main', ?, ?, 10, 1, ?, ?)
            """.trimIndent(),
            snapshot,
            id,
            source,
            step,
            agentId,
            "a".repeat(64),
            "snap-$snapshot",
            stamp(T0.minus(Duration.ofDays(900))),
            if (forgotten) stamp(T0.minus(Duration.ofDays(500))) else null,
        )
        return snapshot
    }

    /** A delivery about [run] in [status]; the term of a final one ended at [finishedAt]. */
    fun delivery(
        run: UUID,
        createdAt: Instant,
        status: String = "delivered",
        channel: String = "telegram",
    ): UUID {
        val delivery = UUID.randomUUID()
        jdbc.update(
            """
            insert into notification_deliveries (id, tenant_id, run_id, channel, status, attempts, next_attempt_at,
                                                 created_at, finished_at)
            values (?, ?, ?, ?, ?, 1, ?, ?, ?)
            """.trimIndent(),
            delivery,
            id,
            run,
            channel,
            status,
            stamp(createdAt),
            stamp(createdAt),
            if (status == "pending") null else stamp(createdAt),
        )
        return delivery
    }

    /** [count] deliveries, each about a run of its own, all created at [createdAt]. */
    fun deliveries(
        source: UUID,
        count: Int,
        createdAt: Instant,
        status: String = "delivered",
    ) {
        val marker = UUID.randomUUID().toString()
        jdbc.update(
            """
            insert into runs (id, tenant_id, source_id, trigger, status, queued_at, started_at, finished_at, message)
            select gen_random_uuid(), ?, ?, 'manual', 'succeeded', ?::timestamptz, ?::timestamptz, ?::timestamptz, ?
            from generate_series(1, ?)
            """.trimIndent(),
            id,
            source,
            stamp(createdAt),
            stamp(createdAt),
            stamp(createdAt),
            marker,
            count,
        )
        jdbc.update(
            """
            insert into notification_deliveries (id, tenant_id, run_id, channel, status, attempts, next_attempt_at,
                                                 created_at, finished_at)
            select gen_random_uuid(), tenant_id, id, 'telegram', ?, 1, ?::timestamptz, ?::timestamptz,
                   case when ? = 'pending' then null else ?::timestamptz end
            from runs where tenant_id = ? and message = ?
            """.trimIndent(),
            status,
            stamp(createdAt),
            stamp(createdAt),
            status,
            stamp(createdAt),
            id,
            marker,
        )
    }

    fun schedule(source: UUID): UUID {
        val schedule = UUID.randomUUID()
        jdbc.update(
            """
            insert into schedules (id, tenant_id, source_id, cron, timezone, enabled, next_run_at, created_at,
                                   updated_at)
            values (?, ?, ?, '0 3 * * *', 'UTC', true, ?, ?, ?)
            """.trimIndent(),
            schedule,
            id,
            source,
            stamp(T0.plus(Duration.ofDays(1))),
            stamp(T0.minus(Duration.ofDays(900))),
            stamp(T0.minus(Duration.ofDays(900))),
        )
        return schedule
    }

    /** A fire of [schedule] recorded at [at]; with [run] it is `skipped_active` (or `run_created`). */
    fun fire(
        schedule: UUID,
        at: Instant,
        run: UUID? = null,
        kind: String = "schedule",
        alert: Boolean = false,
        catchUpOf: UUID? = null,
    ): UUID {
        val fire = UUID.randomUUID()
        val downtime = catchUpOf != null
        val outcome =
            when {
                downtime -> "skipped_downtime"
                run != null -> "skipped_active"
                else -> "skipped_gone"
            }
        jdbc.update(
            """
            insert into schedule_fires (id, tenant_id, schedule_id, kind, scheduled_for, outcome, run_id, reason,
                                        missed_count, missed_until, skipped_in_row, alert, recorded_at,
                                        catch_up_fire_id)
            values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?, ?)
            """.trimIndent(),
            fire,
            id,
            schedule,
            if (downtime) "schedule" else kind,
            stamp(at.minusMillis(60_000L + ++fires)),
            outcome,
            run,
            if (outcome == "skipped_gone") "source_deleted" else null,
            if (downtime) 2 else null,
            if (downtime) stamp(at) else null,
            alert,
            stamp(at),
            catchUpOf,
        )
        return fire
    }

    private var fires = 0

    /** [count] fires of [schedule], all recorded at [at]. */
    fun fires(
        schedule: UUID,
        count: Int,
        at: Instant,
    ) {
        repeat(count) { fire(schedule, at) }
    }

    /** A verification job of [source] on [snapshot], and with [verification] status its record. */
    fun job(
        source: UUID,
        snapshot: UUID,
        status: String = "closed",
        run: UUID? = null,
        at: Instant = T0.minus(Duration.ofDays(400)),
    ): UUID {
        val job = UUID.randomUUID()
        jdbc.update(
            """
            insert into verification_jobs (id, tenant_id, source_id, snapshot_ref, repository_id, plugin, reason,
                                           status,
                                           run_id, created_at, deadline, closed_at)
            values (?, ?, ?, ?, ?, 'postgresql', 'cycle', ?, ?, ?, ?, ?)
            """.trimIndent(),
            job,
            id,
            source,
            snapshot,
            "a".repeat(64),
            status,
            run,
            stamp(at),
            stamp(at.plusSeconds(3600)),
            if (status == "closed") stamp(at) else null,
        )
        return job
    }

    /**
     * A verification record of [snapshot] verified at [at], with its closed job, which [run] (a verification run)
     * ran when it is given, and the step of that run. [evidence] is the JSON of its stages and its mismatches.
     */
    fun verification(
        source: UUID,
        snapshot: UUID,
        at: Instant,
        status: String = "passed",
        run: SceneRun? = null,
        jobStatus: String = "closed",
        evidence: String = "[]",
    ): UUID {
        // A verdict other than no_verifier states the step it ran; a record without a run of its own gets a failed one.
        val ran = run ?: run(source, at, status = "failed", trigger = "verification")
        val job = job(source, snapshot, jobStatus, ran.id, at)
        val verification = UUID.randomUUID()
        jdbc.update(
            """
            insert into restore_verifications (id, tenant_id, job_id, source_id, snapshot_id, step_id,
                                               verifier_agent_id,
                                               key_holders, status, level, verified_at, stages, mismatches,
                                               cancelled_by, cancelled_at)
            values (?, ?, ?, ?, ?, ?, ?, 1, ?, ?, ?, ?::jsonb, ?::jsonb, ?, ?)
            """.trimIndent(),
            verification,
            id,
            job,
            source,
            snapshot,
            ran.steps.first(),
            agentId,
            status,
            if (status == "passed") 4 else 1,
            stamp(at),
            evidence,
            evidence,
            if (status == "cancelled") "admin" else null,
            if (status == "cancelled") stamp(at) else null,
        )
        return verification
    }

    /**
     * [count] verifications of one snapshot, each with a closed job, all verified at [at] and all stating the step of
     * one verification run of their own.
     */
    fun verifications(
        source: UUID,
        snapshot: UUID,
        count: Int,
        at: Instant,
        status: String = "passed",
    ) {
        val step = run(source, at, status = "succeeded", trigger = "verification").steps.single()
        jdbc.update(
            """
            insert into verification_jobs (id, tenant_id, source_id, snapshot_ref, repository_id, plugin, reason,
                                           status,
                                           created_at, deadline, closed_at)
            select gen_random_uuid(), ?, ?, ?, ?, 'postgresql', 'cycle', 'closed', ?::timestamptz, ?::timestamptz,
                   ?::timestamptz
            from generate_series(1, ?)
            """.trimIndent(),
            id,
            source,
            snapshot,
            "a".repeat(64),
            stamp(at),
            stamp(at),
            stamp(at),
            count,
        )
        jdbc.update(
            """
            insert into restore_verifications (id, tenant_id, job_id, source_id, snapshot_id, step_id,
                                               verifier_agent_id,
                                               key_holders, status, level, verified_at)
            select gen_random_uuid(), j.tenant_id, j.id, j.source_id, j.snapshot_ref, ?, ?, 1, ?,
                   case when ? = 'passed' then 4 else 1 end, ?::timestamptz
            from verification_jobs j
            where j.tenant_id = ? and j.source_id = ? and j.created_at = ?::timestamptz
              and not exists (select 1 from restore_verifications v where v.tenant_id = j.tenant_id and v.job_id = j.id)
            """.trimIndent(),
            step,
            agentId,
            status,
            status,
            stamp(at),
            id,
            source,
            stamp(at),
        )
    }

    /** A closed stale-verification episode that the [verification] closed. */
    fun staleEpisode(
        source: UUID,
        closedBy: UUID?,
        openedAt: Instant = T0.minus(Duration.ofDays(500)),
    ): UUID {
        val episode = UUID.randomUUID()
        jdbc.update(
            """
            insert into verification_stale_episodes (id, tenant_id, source_id, counted_from, opened_at, closed_at,
                                                     closed_by_id)
            values (?, ?, ?, ?, ?, ?, ?)
            """.trimIndent(),
            episode,
            id,
            source,
            stamp(openedAt.minusSeconds(3600)),
            stamp(openedAt),
            closedBy?.let { stamp(openedAt.plusSeconds(60)) },
            closedBy,
        )
        return episode
    }

    /** An overdue episode closed by the success [run]. */
    fun overdueEpisode(
        source: UUID,
        closedBy: UUID,
    ): UUID {
        val episode = UUID.randomUUID()
        jdbc.update(
            """
            insert into overdue_episodes (id, tenant_id, source_id, deadline, opened_at, closed_at, closed_by_run_id)
            values (?, ?, ?, ?, ?, ?, ?)
            """.trimIndent(),
            episode,
            id,
            source,
            stamp(T0.minus(Duration.ofDays(400))),
            stamp(T0.minus(Duration.ofDays(400))),
            stamp(T0.minus(Duration.ofDays(399))),
            closedBy,
        )
        return episode
    }

    /** [count] log lines of [step] received at [at], in the partition of that month. */
    fun logLines(
        step: UUID,
        count: Int,
        at: Instant,
    ) {
        jdbc.update(
            """
            insert into step_logs (tenant_id, step_id, seq, received_at, level, text)
            select ?, ?, s, ?::timestamptz, 'info', 'line ' || s from generate_series(1, ?) s
            """.trimIndent(),
            id,
            step,
            stamp(at),
            count,
        )
    }

    /** A delivered notification that the overdue [episode] opened. */
    fun episodeDelivery(
        episode: UUID,
        createdAt: Instant,
    ): UUID {
        val delivery = UUID.randomUUID()
        jdbc.update(
            """
            insert into notification_deliveries (id, tenant_id, episode_id, episode_event, channel, status, attempts,
                                                 next_attempt_at, created_at, finished_at)
            values (?, ?, ?, 'opened', 'telegram', 'delivered', 1, ?, ?, ?)
            """.trimIndent(),
            delivery,
            id,
            episode,
            stamp(createdAt),
            stamp(createdAt),
            stamp(createdAt),
        )
        return delivery
    }

    /** A recorded certificate event of the agent and a delivered notification about it; the event's id. */
    fun certificateEventDelivery(createdAt: Instant): UUID {
        val serial = UUID.randomUUID().toString().replace("-", "")
        val event = UUID.randomUUID()
        jdbc.update(
            "insert into agent_certificates (serial, tenant_id, agent_id, issued_at, not_after) values (?, ?, ?, ?, ?)",
            serial,
            id,
            agentId,
            stamp(T0.minus(Duration.ofDays(900))),
            stamp(T0.minus(Duration.ofDays(500))),
        )
        jdbc.update(
            "insert into agent_certificate_events (id, tenant_id, serial, agent_id, event, recorded_at) " +
                "values (?, ?, ?, ?, 'expired', ?)",
            event,
            id,
            serial,
            agentId,
            stamp(createdAt),
        )
        jdbc.update(
            """
            insert into notification_deliveries (id, tenant_id, certificate_event_id, channel, status, attempts,
                                                 next_attempt_at, created_at, finished_at)
            values (?, ?, ?, 'telegram', 'delivered', 1, ?, ?, ?)
            """.trimIndent(),
            UUID.randomUUID(),
            id,
            event,
            stamp(createdAt),
            stamp(createdAt),
            stamp(createdAt),
        )
        return event
    }

    fun fireDelivery(
        fire: UUID,
        createdAt: Instant,
    ) {
        jdbc.update(
            """
            insert into notification_deliveries (id, tenant_id, fire_id, channel, status, attempts, next_attempt_at,
                                                 created_at, finished_at)
            values (?, ?, ?, 'telegram', 'delivered', 1, ?, ?, ?)
            """.trimIndent(),
            UUID.randomUUID(),
            id,
            fire,
            stamp(createdAt),
            stamp(createdAt),
            stamp(createdAt),
        )
    }

    fun count(
        table: String,
        where: String = "true",
    ): Int {
        val sql = "select count(*) from $table where tenant_id = ? and ($where)"
        return jdbc.queryForObject(sql, Int::class.java, id) ?: 0
    }

    fun exists(
        table: String,
        row: UUID,
    ): Boolean = count(table, "id = '$row'") == 1

    /** Through the superuser: sard_app may not delete the records of verifications (DB2). */
    fun drop() {
        val admin = TestAdmin.of(jdbc)
        for (table in TABLES) admin.update("delete from $table where tenant_id = ?", id)
        owner?.drop()
    }

    private companion object {
        /** Children first. */
        val TABLES =
            listOf(
                "notification_deliveries",
                "agent_certificate_events",
                "agent_certificates",
                "overdue_episodes",
                "verification_stale_episodes",
                "restore_verifications",
                "verification_jobs",
                "schedule_fires",
                "schedules",
                "snapshots",
                "step_logs",
            )
    }
}

internal fun stamp(at: Instant): Timestamp = Timestamp.from(at)

/** The partitions the other tests of the same database expect, which a pass of the time of the test has dropped. */
internal fun restorePartitions(partitions: StepLogPartitions) {
    partitions.ensure(Instant.now(), 2)
    partitions.ensure(RUNS_NOW, 2)
}

/** The names of the partitions of `step_logs`. */
internal fun JdbcTemplate.partitionNames(): List<String> =
    queryForList(
        "select c.relname from pg_inherits i join pg_class c on c.oid = i.inhrelid " +
            "where i.inhparent = 'step_logs'::regclass",
        String::class.java,
    ).filterNotNull()

/** The partition `step_logs_YYYY_MM` of a month, if it is not there yet, made by the function the server uses. */
internal fun JdbcTemplate.createPartition(month: String) {
    val (year, number) = month.split("_").map { it.toInt() }
    queryForObject(
        "select create_step_log_partition(?)",
        String::class.java,
        java.sql.Date.valueOf(LocalDate.of(year, number, 1)),
    )
}

/** Drops the partitions of `step_logs` the way a pass does, with the function of the database. */
internal fun JdbcTemplate.dropPartitions() {
    for (name in partitionNames()) queryForObject("select drop_step_log_partition(?)", Boolean::class.java, name)
}
