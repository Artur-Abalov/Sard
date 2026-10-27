// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.verify

import dev.sard.server.persistence.PageKey
import dev.sard.server.persistence.TenantSessions
import dev.sard.server.runs.SourceNotFound
import dev.sard.server.runs.StepOutput
import dev.sard.server.runs.VerificationRuns
import org.hibernate.Session
import tools.jackson.databind.json.JsonMapper
import java.time.Clock
import java.time.Duration
import java.time.Instant
import java.util.UUID

private val JSON = JsonMapper.builder().build()

// Native SQL names tenant_id explicitly (ADR 0013, rule 8).
private const val COLUMNS = """
    select rv.id, rv.job_id, rv.source_id, rv.snapshot_id, sn.snapshot_id, rv.step_id, rv.verifier_agent_id,
        rv.key_holders, rv.status, rv.level, rv.reason, cast(rv.stages as text), cast(rv.mismatches as text),
        cast(rv.unsuitable as text), rv.restore_duration_ms, rv.recovered, rv.verified_at, sn.fingerprint,
        sn.fingerprint_sha256
    from restore_verifications rv join snapshots sn on sn.tenant_id = rv.tenant_id and sn.id = rv.snapshot_id"""
private const val ONE = "$COLUMNS where rv.tenant_id = :tenant and rv.id = :id"
private const val OF_SOURCE = "$COLUMNS where rv.tenant_id = :tenant and rv.source_id = :source"
private const val PAGE_AFTER = " and (rv.verified_at, rv.id) < (:at, :after)"
private const val NEWEST_FIRST = " order by rv.verified_at desc, rv.id desc limit :limit"
private const val LIVE_SOURCE =
    "select id, true from sources where tenant_id = :tenant and id = :id and deleted_at is null"

// What a source's latest whole snapshot asks of a verifier: as the queue judges a job of it.
private const val NEEDS = """
    select sn.repository_id, s.plugin, sn.total_bytes, sa.machine_id, na.machine_id from sources s
    join agents sa on sa.tenant_id = s.tenant_id and sa.id = s.agent_id
    join lateral (select repository_id, total_bytes, agent_id from snapshots
        where tenant_id = s.tenant_id and source_id = s.id and not partial and forgotten_at is null
        order by created_at desc, id desc limit 1) sn on true
    join agents na on na.tenant_id = s.tenant_id and na.id = sn.agent_id
    where s.tenant_id = :tenant and s.id = :id and s.deleted_at is null"""
private const val SANDBOXES =
    "select id, verify_sandbox, verify_sandbox_version from agents " +
        "where tenant_id = :tenant and role = 'verifier' and revoked_at is null"

// What each verifier did lately (V6, ОВ8): its records, and among them its own failures.
private const val COUNTERS = """
    select verifier_agent_id, count(*), count(*) filter (where status = 'infrastructure_failure')
    from restore_verifications
    where tenant_id = :tenant and verifier_agent_id is not null and verified_at >= :since
    group by verifier_agent_id"""

/** How far back the counters of a verifier go. */
private val COUNTER_WINDOW: Duration = Duration.ofDays(30)

/** One verification as recorded, with its evidence. */
data class VerificationView(
    val id: UUID,
    val jobId: UUID,
    val sourceId: UUID,
    /** snapshots.id. */
    val snapshotRef: UUID,
    /** restic's snapshot id. */
    val snapshotId: String,
    val stepId: UUID?,
    val verifierId: UUID?,
    val keyHolders: Int,
    val status: VerificationStatus,
    val level: Int,
    val reason: String?,
    val stages: List<StepOutput.Stage>,
    val mismatches: List<StepOutput.Mismatch>,
    val unsuitable: List<Misfit>?,
    val restoreMillis: Long?,
    /** From the first stage's start to the last stage's finish; null without stage times. */
    val durationMillis: Long?,
    val recovered: Boolean,
    val verifiedAt: Instant,
    /** The source's fingerprint beside the copy's; empty when the snapshot kept none. */
    val fingerprint: FingerprintView = FingerprintView.NONE,
)

/** A live verifier of the tenant, what it reported, and why it does not fit a source's job (when asked). */
data class VerifierView(
    val candidate: Candidate,
    /** none, docker, podman; null when unknown. */
    val sandbox: String?,
    /** The runtime's version as the verifier reported it; null when unknown. */
    val sandboxVersion: String?,
    /** Records of verifications it did in the last 30 days (ОВ8). */
    val done: Int,
    /** Of those, the ones that ended as its own infrastructure failure. */
    val infrastructureFailures: Int,
    /** Null when no source was asked about. */
    val reasons: Set<Unsuitable>?,
)

/** What REST reads of restore verification in a tenant (V4): history, one record, the verifiers. */
class VerificationReads(
    private val sessions: TenantSessions,
    private val clock: Clock,
    private val pool: VerifierPool,
    private val settings: VerificationSettings,
) {
    fun get(
        tenantId: UUID,
        id: UUID,
    ): VerificationView? =
        sessions.inTenant(tenantId) { session ->
            session
                .createNativeQuery(ONE.trimIndent(), Array<Any?>::class.java)
                .setParameter("tenant", tenantId)
                .setParameter("id", id)
                .list()
                .firstOrNull()
                ?.let(::viewOf)
        }

    /** The source's verifications, newest first, after [after] if given, at most [limit]. */
    fun history(
        tenantId: UUID,
        sourceId: UUID,
        after: PageKey?,
        limit: Int,
    ): List<VerificationView> =
        sessions.inTenant(tenantId) { session ->
            requireLive(session, tenantId, sourceId)
            val sql = OF_SOURCE.trimIndent() + (if (after == null) "" else PAGE_AFTER) + NEWEST_FIRST
            val query =
                session
                    .createNativeQuery(sql, Array<Any?>::class.java)
                    .setParameter("tenant", tenantId)
                    .setParameter("source", sourceId)
                    .setParameter("limit", limit)
            after?.let { query.setParameter("at", it.at).setParameter("after", it.id) }
            query.list().map(::viewOf)
        }

    /**
     * The tenant's live verifiers; with [sourceId], why each one would not take a job of the source's latest whole
     * snapshot now (empty set: it would) — for the console of V6 and for debugging.
     */
    fun verifiers(
        tenantId: UUID,
        sourceId: UUID?,
    ): List<VerifierView> =
        sessions.inTenant(tenantId) { session ->
            val candidates = pool.candidates(session, tenantId, clock.instant() - settings.recentWindow)
            val facts = VerifierFacts(sandboxes(session, tenantId), counters(session, tenantId))
            val needs = sourceId?.let { needs(session, tenantId, it) }
            candidates.map { facts.view(it, if (sourceId == null) null else reasonsFor(needs, it)) }
        }

    private fun reasonsFor(
        needs: JobNeeds?,
        candidate: Candidate,
    ): Set<Unsuitable> {
        // A source without a whole snapshot has no job: nothing would be handed out, to anyone.
        if (needs == null) return emptySet()
        return when (val match = VerifierChoice.of(needs, listOf(candidate))) {
            is Match.Chosen -> match.warnings
            is Match.NoneFits -> match.misfits.single().reasons
        }
    }

    /** The needs of the source's latest whole snapshot; null without one; [SourceNotFound] for no live source. */
    private fun needs(
        session: Session,
        tenantId: UUID,
        sourceId: UUID,
    ): JobNeeds? {
        requireLive(session, tenantId, sourceId)
        val row =
            session
                .createNativeQuery(NEEDS.trimIndent(), Array<Any?>::class.java)
                .setParameter("tenant", tenantId)
                .setParameter("id", sourceId)
                .list()
                .firstOrNull() ?: return null
        val machines = setOfNotNull(row[3] as String?, row[4] as String?)
        return JobNeeds(row[0] as String, row[1] as String, machines, settings.space.of((row[2] as Number).toLong()))
    }

    private fun sandboxes(
        session: Session,
        tenantId: UUID,
    ): Map<UUID, Pair<String?, String?>> =
        session
            .createNativeQuery(SANDBOXES, Array<Any?>::class.java)
            .setParameter("tenant", tenantId)
            .list()
            .associate { it[0] as UUID to (it[1] as String? to it[2] as String?) }

    private fun counters(
        session: Session,
        tenantId: UUID,
    ): Map<UUID, Pair<Int, Int>> =
        session
            .createNativeQuery(COUNTERS.trimIndent(), Array<Any?>::class.java)
            .setParameter("tenant", tenantId)
            .setParameter("since", clock.instant() - COUNTER_WINDOW)
            .list()
            .associate { it[0] as UUID to ((it[1] as Number).toInt() to (it[2] as Number).toInt()) }

    private fun requireLive(
        session: Session,
        tenantId: UUID,
        sourceId: UUID,
    ) {
        val live =
            session
                .createNativeQuery(LIVE_SOURCE, Array<Any?>::class.java)
                .setParameter("tenant", tenantId)
                .setParameter("id", sourceId)
                .list()
        if (live.isEmpty()) throw SourceNotFound(sourceId)
    }
}

/** What the agents table and the history say of the verifiers, by agent id. */
private class VerifierFacts(
    private val sandboxes: Map<UUID, Pair<String?, String?>>,
    private val counters: Map<UUID, Pair<Int, Int>>,
) {
    fun view(
        candidate: Candidate,
        reasons: Set<Unsuitable>?,
    ): VerifierView {
        val (runtime, version) = sandboxes[candidate.agentId] ?: (null to null)
        val (done, failures) = counters[candidate.agentId] ?: (0 to 0)
        return VerifierView(candidate, runtime, version, done, failures, reasons)
    }
}

/** The columns of [COLUMNS], in its select order. */
private enum class Column {
    ID,
    JOB,
    SOURCE,
    SNAPSHOT_REF,
    SNAPSHOT_ID,
    STEP,
    VERIFIER,
    KEY_HOLDERS,
    STATUS,
    LEVEL,
    REASON,
    STAGES,
    MISMATCHES,
    UNSUITABLE,
    RESTORE,
    RECOVERED,
    VERIFIED_AT,
    FINGERPRINT,
    FINGERPRINT_SHA256,
}

private operator fun Array<Any?>.get(column: Column): Any? = this[column.ordinal]

/** A row of [COLUMNS]. */
private fun viewOf(row: Array<Any?>): VerificationView {
    val status = VerificationStatus.of(row[Column.STATUS] as String)
    val stages = VerificationRuns.stagesOf(row[Column.STAGES] as String)
    val mismatches = VerificationRuns.mismatchesOf(row[Column.MISMATCHES] as String)
    val fingerprint =
        FingerprintComparison.view(
            row[Column.FINGERPRINT] as ByteArray?,
            row[Column.FINGERPRINT_SHA256] as String?,
            mismatches,
            stages,
        )
    return VerificationView(
        id = row[Column.ID] as UUID,
        jobId = row[Column.JOB] as UUID,
        sourceId = row[Column.SOURCE] as UUID,
        snapshotRef = row[Column.SNAPSHOT_REF] as UUID,
        snapshotId = row[Column.SNAPSHOT_ID] as String,
        stepId = row[Column.STEP] as UUID?,
        verifierId = row[Column.VERIFIER] as UUID?,
        keyHolders = (row[Column.KEY_HOLDERS] as Number).toInt(),
        status = status,
        level = (row[Column.LEVEL] as Number).toInt(),
        reason = row[Column.REASON] as String?,
        stages = stages,
        mismatches = mismatches,
        unsuitable = (row[Column.UNSUITABLE] as String?)?.let(::misfitsOf),
        restoreMillis = (row[Column.RESTORE] as Number?)?.toLong(),
        durationMillis = ReportDuration.millis(stages),
        recovered = row[Column.RECOVERED] as Boolean,
        verifiedAt = row[Column.VERIFIED_AT] as Instant,
        fingerprint = fingerprint,
    )
}

/** The stored form of [VerificationHistory.misfitsJson]. */
internal fun misfitsOf(json: String): List<Misfit> {
    val array = JSON.readTree(json)
    return (0 until array.size()).map(array::get).map { node ->
        val reasons = node.path("reasons")
        Misfit(
            UUID.fromString(node.path("agentId").asString()),
            node.path("hostname").asString(),
            (0 until reasons.size()).map { Unsuitable.of(reasons.get(it).asString()) }.toSet(),
        )
    }
}
