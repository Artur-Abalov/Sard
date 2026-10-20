// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.runs

import tools.jackson.databind.json.JsonMapper
import java.time.Instant
import java.util.Base64
import java.util.UUID

private val JSON = JsonMapper.builder().build()

/**
 * What a VERIFY step carries besides its snapshot (proto RunStep.verify, V4), stored in run_steps.verify_task as
 * sent. No secret: the verifier opens the repository with its own key (ADR 0008).
 */
class VerifyTask(
    val jobId: UUID,
    /** restic's id of the repository. */
    val repositoryId: String,
    /** The source's fingerprint as its backup reported it; null until V2 brings one. */
    val fingerprint: ByteArray?,
    /** Lower-case hex SHA-256 of [fingerprint]; null without one. */
    val fingerprintSha256: String?,
    val deadline: Instant,
    /** RunStep.timeout: the agent stops the step after it. */
    val timeoutSeconds: Long,
) {
    fun json(): String =
        JSON.writeValueAsString(
            mapOf(
                "jobId" to jobId.toString(),
                "repositoryId" to repositoryId,
                "fingerprint" to fingerprint?.let { Base64.getEncoder().encodeToString(it) },
                "fingerprintSha256" to fingerprintSha256,
                "deadline" to deadline.toString(),
                "timeoutSeconds" to timeoutSeconds,
            ),
        )

    override fun equals(other: Any?): Boolean =
        other is VerifyTask &&
            listOf(jobId, repositoryId, fingerprintSha256, deadline, timeoutSeconds) ==
            listOf(other.jobId, other.repositoryId, other.fingerprintSha256, other.deadline, other.timeoutSeconds) &&
            fingerprint.contentEquals(other.fingerprint)

    override fun hashCode(): Int = listOf(jobId, repositoryId, deadline).hashCode()

    override fun toString() = "VerifyTask(jobId=$jobId, deadline=$deadline)"

    companion object {
        /** The task stored by [json]; null for a step without one. */
        fun of(json: String?): VerifyTask? =
            json?.let { JSON.readTree(it) }?.let { node ->
                VerifyTask(
                    UUID.fromString(node.path("jobId").asString()),
                    node.path("repositoryId").asString(),
                    node
                        .path("fingerprint")
                        .asStringOpt()
                        .orElse(null)
                        ?.let { Base64.getDecoder().decode(it) },
                    node.path("fingerprintSha256").asStringOpt().orElse(null),
                    Instant.parse(node.path("deadline").asString()),
                    node.path("timeoutSeconds").asLong(),
                )
            }
    }
}
