// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.runs

import dev.sard.server.persistence.RunRecord
import dev.sard.server.persistence.RunStepRecord
import dev.sard.server.persistence.UuidV7
import org.hibernate.Session
import tools.jackson.databind.JsonNode
import tools.jackson.databind.json.JsonMapper
import java.time.Instant
import java.util.UUID

private val JSON = JsonMapper.builder().build()
private const val NO_CONFIG = "{}"

/** One attempt of a verification job (V4): who verifies what, under which local repository name. */
data class VerificationAttempt(
    val sourceId: UUID,
    val verifierId: UUID,
    val plugin: String,
    /** The verifier's own name of the repository; it reaches the same restic id. */
    val repositoryName: String,
    /** restic's snapshot id. */
    val snapshotId: String,
    val task: VerifyTask,
)

/**
 * A verification attempt is a run of its own (V4, owner decision 1): trigger verification, one VERIFY step on the
 * verifier, so dispatch, progress, results and step loss are those of every run. It is not a backup: D6 does not
 * count it, so it never waits for one and never holds one up.
 */
class VerificationRuns(
    private val ids: UuidV7,
    private val events: RunEvents,
) {
    /** Creates the run and its queued step inside the caller's transaction; returns the run's id. */
    fun start(
        session: Session,
        attempt: VerificationAttempt,
        now: Instant,
    ): UUID {
        val run = RunRecord(ids.next(), attempt.sourceId, Trigger.VERIFICATION.stored, RunState.QUEUED.stored, now)
        session.persist(run)
        session.persist(
            RunStepRecord(
                id = ids.next(),
                runId = run.id,
                ordinal = 0,
                agentId = attempt.verifierId,
                sourceId = attempt.sourceId,
                plugin = attempt.plugin,
                action = Action.VERIFY.stored,
                repositoryName = attempt.repositoryName,
                // The verifier reaches no production system (design document): the source's config is none of its
                // business, everything it checks against comes in the fingerprint (owner decision, V4).
                config = NO_CONFIG,
                status = StepState.QUEUED.stored,
                queuedAt = now,
                snapshotId = attempt.snapshotId,
                verifyTask = attempt.task.json(),
            ),
        )
        session.flush()
        events.created(session, run)
        return run.id
    }

    companion object {
        /** The verification output stored in run_steps.output ([StepOutput.json]); null for none or another kind. */
        fun outputOf(json: String?): StepOutput.Verify? =
            json
                ?.let { JSON.readTree(it) }
                ?.takeIf { it.path("kind").asString() == Action.VERIFY.stored }
                ?.let(::verifyOf)

        private fun verifyOf(node: JsonNode) =
            StepOutput.Verify(
                node.path("snapshotId").asString(),
                emptyList(),
                items(node.path("stages")).map(::stageOf),
                node
                    .path("verdict")
                    .asStringOpt()
                    .orElse(null)
                    ?.let(VerifierVerdict::of),
                items(node.path("mismatches")).map(::mismatchOf),
                node.path("restoreMillis").takeIf { it.isNumber }?.asLong(),
            )

        /** Stages stored as a JSON array of [StepOutput.Stage.fields] (restore_verifications.stages). */
        fun stagesOf(json: String): List<StepOutput.Stage> = items(JSON.readTree(json)).map(::stageOf)

        /** Mismatches stored as a JSON array of [StepOutput.Mismatch.fields]. */
        fun mismatchesOf(json: String): List<StepOutput.Mismatch> = items(JSON.readTree(json)).map(::mismatchOf)

        private fun mismatchOf(node: JsonNode) =
            StepOutput.Mismatch(
                node.path("name").asString(),
                node.path("expected").asString(),
                node.path("actual").asString(),
            )

        private fun items(array: JsonNode): List<JsonNode> = (0 until array.size()).map(array::get)

        private fun stageOf(node: JsonNode) =
            StepOutput.Stage(
                node.path("level").asInt(),
                node.path("name").asString(),
                node
                    .path("state")
                    .asStringOpt()
                    .orElse(null)
                    ?.let(StageState::of),
                node
                    .path("startedAt")
                    .asStringOpt()
                    .orElse(null)
                    ?.let(Instant::parse),
                node
                    .path("finishedAt")
                    .asStringOpt()
                    .orElse(null)
                    ?.let(Instant::parse),
                node.path("exitCode").asInt(),
                node.path("output").asString(),
                node.path("imageDigest").asString(),
            )
    }
}
