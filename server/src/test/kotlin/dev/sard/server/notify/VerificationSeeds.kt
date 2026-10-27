// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.notify

import dev.sard.server.pki.MovableClock
import dev.sard.server.runs.RUNS_NOW
import dev.sard.server.runs.Runs
import dev.sard.server.runs.RunsTenant
import dev.sard.server.runs.Sources
import org.springframework.jdbc.core.JdbcTemplate
import java.sql.Timestamp
import java.time.Duration
import java.time.Instant
import java.util.UUID

/** What a seeded record carries besides its status; the level of a pass is 5 unless told otherwise. */
internal data class Evidence(
    val level: Int? = null,
    val mismatches: String = "[]",
    val stages: String = "[]",
    val reason: String? = null,
    val restoreMillis: Long? = null,
    val jobReason: String = "cycle",
)

/** A source of the tenant, its snapshot and the step that made it. */
internal class Watched(
    val id: UUID,
    val snapshot: UUID,
    val step: UUID,
)

/** The rows the restore verification notification tests start from: tenant, verifier, sources, records, episodes. */
internal class VerificationSeeds(
    private val jdbc: JdbcTemplate,
    private val sources: Sources,
    private val runs: Runs,
    private val clock: MovableClock,
) {
    val tenant = RunsTenant(jdbc)
    val verifier: UUID = UUID.randomUUID()

    fun addVerifier() {
        jdbc.update(
            "insert into agents (id, tenant_id, hostname, registered_at, role) " +
                "values (?, ?, 'vrf1.example.com', ?, 'verifier')",
            verifier,
            tenant.id,
            Timestamp.from(RUNS_NOW),
        )
    }

    fun at(minutes: Long): Instant = RUNS_NOW.plus(Duration.ofMinutes(minutes))

    fun watched(name: String = "db-main"): Watched {
        val source = sources.create(tenant.id, tenant.draft(name)).id
        val run = runs.start(tenant.id, source).id
        // The backup behind the snapshot is not what these tests are about: no message of a run.
        jdbc.update("update runs set trigger = 'verification' where id = ?", run)
        tenant.finish(run)
        val step = jdbc.queryForObject("select id from run_steps where run_id = ?", UUID::class.java, run)!!
        val snapshot = UUID.randomUUID()
        jdbc.update(
            "insert into snapshots (id, tenant_id, source_id, step_id, agent_id, repository_name, repository_id, " +
                "snapshot_id, total_bytes, added_bytes, created_at, partial) " +
                "values (?, ?, ?, ?, ?, 'main', ?, ?, 10, 1, ?, false)",
            snapshot,
            tenant.id,
            source,
            step,
            tenant.agentId,
            "c".repeat(64),
            "4f3a9c1e-$snapshot",
            Timestamp.from(RUNS_NOW),
        )
        return Watched(source, snapshot, step)
    }

    fun record(
        source: Watched,
        status: String,
        minutes: Long,
        evidence: Evidence = Evidence(),
    ): UUID {
        val job = UUID.randomUUID()
        val record = UUID.randomUUID()
        val time = Timestamp.from(at(minutes))
        jdbc.update(
            "insert into verification_jobs (id, tenant_id, source_id, snapshot_ref, repository_id, plugin, reason, " +
                "status, created_at, deadline, closed_at) values (?, ?, ?, ?, ?, 'postgresql', ?, 'closed', ?, ?, ?)",
            job,
            tenant.id,
            source.id,
            source.snapshot,
            "c".repeat(64),
            evidence.jobReason,
            time,
            time,
            time,
        )
        jdbc.update(
            "insert into restore_verifications (id, tenant_id, job_id, source_id, snapshot_id, step_id, " +
                "verifier_agent_id, key_holders, status, level, reason, stages, mismatches, restore_duration_ms, " +
                "recovered, verified_at) values (?, ?, ?, ?, ?, ?, ?, 2, ?, ?, ?, cast(? as jsonb), " +
                "cast(? as jsonb), ?, false, ?)",
            record,
            tenant.id,
            job,
            source.id,
            source.snapshot,
            source.step,
            if (status == "no_verifier") null else verifier,
            status,
            evidence.level ?: if (status == "passed") 5 else 0,
            evidence.reason,
            evidence.stages,
            evidence.mismatches,
            evidence.restoreMillis,
            time,
        )
        return record
    }

    fun failedAt(
        source: Watched,
        minutes: Long,
    ) = record(
        source,
        "failed",
        minutes,
        Evidence(
            level = 3,
            mismatches = """[{"name":"rows:public.orders","expected":"1204311","actual":"1204310"}]""",
            stages =
                """[{"level":4,"name":"fingerprint","state":"failed","exitCode":1,""" +
                    """"output":"QA-OUTPUT-MARKER","imageDigest":""}]""",
        ),
    )

    fun episode(
        source: Watched,
        openedMinutes: Long,
        countedFrom: Instant? = at(0),
        closedBy: UUID? = null,
    ): UUID {
        val id = UUID.randomUUID()
        jdbc.update(
            "insert into verification_stale_episodes (id, tenant_id, source_id, counted_from, opened_at, closed_at, " +
                "closed_by_id, threshold_days, timezone) values (?, ?, ?, ?, ?, ?, ?, 2, 'Europe/Berlin')",
            id,
            tenant.id,
            source.id,
            Timestamp.from(countedFrom ?: at(0)),
            Timestamp.from(at(openedMinutes)),
            closedBy?.let { Timestamp.from(clock.now) },
            closedBy,
        )
        return id
    }

    fun statusOf(record: UUID): String? =
        jdbc
            .queryForList(
                "select status from notification_deliveries where verification_id = ?",
                String::class.java,
                record,
            ).singleOrNull()

    fun deliveries(): Int =
        jdbc.queryForObject(
            "select count(*) from notification_deliveries where tenant_id = ?",
            Int::class.java,
            tenant.id,
        )!!
}
