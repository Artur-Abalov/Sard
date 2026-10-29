// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import dev.sard.server.agents.stream.TestAgent
import java.security.MessageDigest
import java.sql.Timestamp
import java.time.Instant
import java.util.UUID

/** The repository id every seeded snapshot carries. */
val SEED_REPOSITORY_ID = "e".repeat(64)

/** One fact of a fingerprint a test seeds. */
data class SeedFact(
    val name: String,
    val rule: String,
    val value: String,
)

/** The canonical bytes of a fingerprint of [facts] and their SHA-256, as a backup stores them (V2). */
fun fingerprintOf(facts: List<SeedFact>): Pair<ByteArray, String> {
    val json =
        facts.joinToString(",", """{"format":1,"plugin":"postgresql","facts":[""", "]}") {
            """{"name":"${it.name}","rule":"${it.rule}","value":"${it.value}"}"""
        }
    val bytes = json.toByteArray()
    return bytes to MessageDigest.getInstance("SHA-256").digest(bytes).joinToString("") { "%02x".format(it) }
}

/**
 * Sources, snapshots and verification records of one tenant written the way the server would have left them (V4),
 * for the tests of the console's reads (V6). Sources are made through the API; the rest goes into the tables.
 */
class VerificationSeed(
    private val world: RestWorld,
    private val tenant: UUID,
    private val session: ApiSession,
    private val verifier: TestAgent,
) {
    private val jdbc = world.jdbc

    /** A live source of the files plugin on [agent]; its verification is on unless [enabled] says otherwise. */
    fun source(
        name: String,
        agent: TestAgent,
        enabled: Boolean = true,
    ): UUID {
        val id =
            UUID.fromString(
                world.api
                    .post("/api/v1/sources", session, sourceJson(name, agent.agentId))
                    .json
                    .path("id")
                    .asString(),
            )
        if (!enabled) jdbc.update("update sources set verification_enabled = false where id = ?", id)
        return id
    }

    /** A whole snapshot of [source] made at [at]; the backup run behind it succeeded. */
    fun snapshot(
        source: UUID,
        agent: TestAgent,
        at: Instant = world.clock.now,
        fingerprint: Pair<ByteArray, String>? = null,
        repositoryId: String = SEED_REPOSITORY_ID,
    ): UUID {
        val run =
            UUID.fromString(
                world.api
                    .post("/api/v1/sources/$source/runs", session, "")
                    .json
                    .path("id")
                    .asString(),
            )
        world.forceRun(run, "succeeded")
        val step = jdbc.queryForObject("select id from run_steps where run_id = ?", UUID::class.java, run)
        val id = UUID.randomUUID()
        jdbc.update(
            "insert into snapshots (id, tenant_id, source_id, step_id, agent_id, repository_name, repository_id, " +
                "snapshot_id, total_bytes, added_bytes, created_at, partial, fingerprint, fingerprint_sha256) " +
                "values (?, ?, ?, ?, ?, 'qa', ?, ?, 10, 1, ?, false, ?, ?)",
            id,
            tenant,
            source,
            step,
            agent.agentId,
            repositoryId,
            "4f3a9c1e7b2d-$id",
            Timestamp.from(at),
            fingerprint?.first,
            fingerprint?.second,
        )
        return id
    }

    /**
     * A closed job and its record. The snapshot is made for the record when [Record.snapshot] is null; a record of a
     * verdict has a step and the seed's verifier, a no_verifier one has no verifier.
     */
    fun record(
        source: UUID,
        agent: TestAgent,
        spec: Record,
    ): UUID {
        val snap = spec.snapshot ?: snapshot(source, agent, spec.at.minusSeconds(60))
        val job = UUID.randomUUID()
        val record = UUID.randomUUID()
        val time = Timestamp.from(spec.at)
        jdbc.update(
            "insert into verification_jobs (id, tenant_id, source_id, snapshot_ref, repository_id, plugin, reason, " +
                "status, created_at, deadline, closed_at) values (?, ?, ?, ?, ?, 'files', 'cycle', 'closed', ?, ?, ?)",
            job,
            tenant,
            source,
            snap,
            SEED_REPOSITORY_ID,
            time,
            time,
            time,
        )
        jdbc.update(
            "insert into restore_verifications (id, tenant_id, job_id, source_id, snapshot_id, step_id, " +
                "verifier_agent_id, key_holders, status, level, reason, stages, mismatches, restore_duration_ms, " +
                "recovered, verified_at, cancelled_by, cancelled_at) values (?, ?, ?, ?, ?, " +
                "(select id from run_steps where tenant_id = ? and source_id = ? limit 1), ?, 2, ?, ?, ?, " +
                "cast(? as jsonb), cast(? as jsonb), ?, ?, ?, ?, ?)",
            record,
            tenant,
            job,
            source,
            snap,
            tenant,
            source,
            if (spec.status == "no_verifier") null else spec.verifiedBy ?: verifier.agentId,
            spec.status,
            spec.level,
            spec.reason,
            spec.stages,
            spec.mismatches,
            spec.restoreMillis,
            spec.recovered,
            time,
            "admin".takeIf { spec.status == "cancelled" },
            time.takeIf { spec.status == "cancelled" },
        )
        return record
    }

    /** What a seeded record says; the level of a pass is 5 unless told otherwise. */
    data class Record(
        val status: String,
        val at: Instant,
        val level: Int = if (status == "passed") 5 else 0,
        val snapshot: UUID? = null,
        val mismatches: String = "[]",
        val stages: String = "[]",
        val reason: String? = null,
        val restoreMillis: Long? = null,
        val recovered: Boolean = false,
        val verifiedBy: UUID? = null,
    )
}
