// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import com.fasterxml.jackson.annotation.JsonProperty
import io.swagger.v3.oas.annotations.Operation
import io.swagger.v3.oas.annotations.media.Schema
import io.swagger.v3.oas.annotations.tags.Tag
import org.springframework.http.HttpStatus
import org.springframework.http.MediaType
import org.springframework.web.bind.annotation.GetMapping
import org.springframework.web.bind.annotation.PathVariable
import org.springframework.web.bind.annotation.PostMapping
import org.springframework.web.bind.annotation.PutMapping
import org.springframework.web.bind.annotation.RequestBody
import org.springframework.web.bind.annotation.RequestMapping
import org.springframework.web.bind.annotation.RequestParam
import org.springframework.web.bind.annotation.ResponseStatus
import org.springframework.web.bind.annotation.RestController
import java.time.Instant
import java.util.UUID

private const val LEVEL =
    "Highest level reached (V-D2): 1 storage integrity, 2 extraction, 3 the application starts, 4 the data matches " +
        "the fingerprint, 5 the restore time is known; 0 when none"
private const val STATUS =
    "passed; failed (the backup is bad); cannot_verify (the verifier's environment could not check it); " +
        "infrastructure_failure (three attempts without a verdict); no_verifier (no verifier fitted)"

/** How a verification ended (restore_verifications.status). */
@Schema(enumAsRef = true, description = STATUS)
enum class VerificationOutcome {
    @JsonProperty("passed")
    PASSED,

    @JsonProperty("failed")
    FAILED,

    @JsonProperty("cannot_verify")
    CANNOT_VERIFY,

    @JsonProperty("infrastructure_failure")
    INFRASTRUCTURE_FAILURE,

    @JsonProperty("no_verifier")
    NO_VERIFIER,
}

/** Why a verifier does not take a job; space_unknown only warns. */
@Schema(enumAsRef = true)
enum class VerifierReason {
    @JsonProperty("offline")
    OFFLINE,

    @JsonProperty("outdated")
    OUTDATED,

    @JsonProperty("no_key")
    NO_KEY,

    @JsonProperty("no_plugin")
    NO_PLUGIN,

    @JsonProperty("same_host")
    SAME_HOST,

    @JsonProperty("low_space")
    LOW_SPACE,

    @JsonProperty("busy")
    BUSY,

    @JsonProperty("space_unknown")
    SPACE_UNKNOWN,
}

/** How a stage of the pipeline ended. */
@Schema(enumAsRef = true)
enum class VerificationStageStatus {
    @JsonProperty("passed")
    PASSED,

    @JsonProperty("failed")
    FAILED,

    @JsonProperty("cannot_run")
    CANNOT_RUN,

    @JsonProperty("not_applicable")
    NOT_APPLICABLE,
}

/** Where a job is: waiting for a verifier, or handed to one. */
@Schema(enumAsRef = true)
enum class VerificationJobStatus {
    @JsonProperty("queued")
    QUEUED,

    @JsonProperty("running")
    RUNNING,
}

/** Why a job was made. */
@Schema(enumAsRef = true)
enum class VerificationJobReason {
    @JsonProperty("cycle")
    CYCLE,

    @JsonProperty("manual")
    MANUAL,
}

/** The container runtime a verifier has. */
@Schema(enumAsRef = true)
enum class SandboxRuntime {
    @JsonProperty("none")
    NONE,

    @JsonProperty("docker")
    DOCKER,

    @JsonProperty("podman")
    PODMAN,
}

@Schema(description = "A verifier that did not fit, with every reason")
data class VerifierMisfit(
    val agentId: UUID,
    val hostname: String,
    val reasons: List<VerifierReason>,
)

@Schema(description = "A verification job that is queued or running")
data class VerificationJob(
    val id: UUID,
    val status: VerificationJobStatus,
    val reason: VerificationJobReason,
    val createdAt: Instant,
    @field:Schema(description = "A job no verifier takes until then ends as no_verifier")
    val deadline: Instant,
    @field:Schema(description = "Attempts in a row without a verdict; the third is an infrastructure failure")
    val failedAttempts: Int,
    @field:Schema(description = "Why each verifier did not fit at the last try; null before the first")
    val unsuitable: List<VerifierMisfit>?,
)

@Schema(description = "A source's restore verification")
data class SourceVerificationState(
    @field:Schema(description = "Whether the daily cycle verifies the source's latest whole snapshot")
    val enabled: Boolean,
    @field:Schema(description = "The last verification's status; no_verifier while the installation has no verifier")
    val status: VerificationOutcome?,
    @field:Schema(description = "The last successful verification; null if never")
    val lastVerifiedAt: Instant?,
    @field:Schema(description = "The level of the last successful verification")
    val lastVerifiedLevel: Int?,
    @field:Schema(description = "Not verified successfully since this moment, for longer than the configured days")
    val staleSince: Instant?,
    val openJob: VerificationJob?,
)

@Schema(description = "Whether the source is verified")
data class SourceVerificationInput(
    val enabled: Boolean,
)

@Schema(description = "What to verify now")
data class VerifyNowInput(
    @field:Schema(description = "A snapshot of the source (Snapshot.id); null: its latest whole one")
    val snapshotId: UUID? = null,
)

@Schema(description = "A stage of the verification pipeline and its evidence")
data class VerificationStage(
    @field:Schema(description = "Level the stage proves; 0 for preparation and cleanup")
    val level: Int,
    val name: String,
    @field:Schema(description = "Null for a status this server does not know")
    val status: VerificationStageStatus?,
    val startedAt: Instant?,
    val finishedAt: Instant?,
    val exitCode: Int,
    @field:Schema(description = "Tail of the command's output, redacted on the verifier")
    val output: String,
    @field:Schema(description = "Digest of the sandbox image; empty without one")
    val imageDigest: String,
)

@Schema(description = "A fact of the fingerprint that differs between the source and the restored copy")
data class FingerprintMismatch(
    val name: String,
    val expected: String,
    val actual: String,
)

@Schema(description = "One restore verification of a snapshot")
data class Verification(
    val id: UUID,
    val jobId: UUID,
    val sourceId: UUID,
    @field:Schema(description = "The snapshot verified (Snapshot.id)")
    val snapshotId: UUID,
    @field:Schema(description = "restic's id of that snapshot")
    val resticSnapshotId: String,
    @field:Schema(description = "The VERIFY step of the attempt that ended the job; null for no_verifier")
    val stepId: UUID?,
    val verifierAgentId: UUID?,
    @field:Schema(description = "Live agents holding a key to the repository then; 1 means a single host (ADR 0008)")
    val keyHolders: Int,
    val status: VerificationOutcome,
    @field:Schema(description = LEVEL)
    val level: Int,
    @field:Schema(description = "Why it did not pass, in words; null when it passed")
    val reason: String?,
    val stages: List<VerificationStage>,
    val mismatches: List<FingerprintMismatch>,
    @field:Schema(description = "no_verifier: why each verifier did not fit")
    val unsuitable: List<VerifierMisfit>?,
    @field:Schema(description = "The actual time to restore (level 5)")
    val restoreDurationMs: Long?,
    @field:Schema(description = "A pass right after a failure")
    val recovered: Boolean,
    val verifiedAt: Instant,
)

@Schema(description = "A page of verifications")
data class VerificationPage(
    val items: List<Verification>,
    @field:Schema(description = CURSOR_NEXT)
    val nextCursor: String?,
)

@Schema(description = "The daily verification cycle of the installation")
data class VerificationScheduleInput(
    @field:Schema(description = "Standard cron: five fields", example = "30 4 * * *")
    val cron: String,
    @field:Schema(description = "An IANA time zone", example = "Europe/Moscow")
    val timezone: String,
)

@Schema(description = "The daily verification cycle of the installation")
data class VerificationSchedule(
    val cron: String,
    val timezone: String,
    @field:Schema(description = "The cycle's next moment")
    val nextRunAt: Instant,
)

@Schema(description = "A verifier agent, what it reported and whether it fits")
data class Verifier(
    val agentId: UUID,
    val hostname: String,
    val online: Boolean,
    @field:Schema(description = "It reported a verifier status: V4 or later")
    val reported: Boolean,
    @field:Schema(description = "Free bytes where it restores; null when unknown")
    val freeBytes: Long?,
    @field:Schema(description = "Null when unknown")
    val sandbox: SandboxRuntime?,
    @field:Schema(description = "restic ids of the repositories it holds a key to")
    val repositoryIds: List<String>,
    @field:Schema(description = "Plugins that verify")
    val verifyPlugins: List<String>,
    val busy: Boolean,
    @field:Schema(description = "Verifications handed to it in the last day")
    val recentVerifications: Int,
    @field:Schema(description = "With sourceId: why it would not take the source's job now (empty: it would)")
    val reasons: List<VerifierReason>?,
)

/** What the verification endpoints do (V4). */
interface VerificationsApi {
    fun getSourceVerification(sourceId: UUID): SourceVerificationState

    fun setSourceVerification(
        sourceId: UUID,
        input: SourceVerificationInput,
    ): SourceVerificationState

    fun listSourceVerifications(
        sourceId: UUID,
        cursor: String?,
        limit: Int,
    ): VerificationPage

    fun verifyNow(
        sourceId: UUID,
        input: VerifyNowInput,
    ): VerificationJob

    fun getVerification(verificationId: UUID): Verification

    fun getVerificationSchedule(): VerificationSchedule

    fun setVerificationSchedule(input: VerificationScheduleInput): VerificationSchedule

    fun listVerifiers(sourceId: UUID?): List<Verifier>
}

@RestController
@RequestMapping("/api/v1", produces = [MediaType.APPLICATION_JSON_VALUE])
@Tag(name = "verifications")
class VerificationsController(
    private val api: VerificationsApi,
) {
    @GetMapping("/sources/{sourceId}/verification")
    @ResponseStatus(HttpStatus.OK)
    @Operation(summary = "The source's restore verification")
    @NotFound
    fun getSourceVerification(
        @PathVariable sourceId: UUID,
    ) = api.getSourceVerification(sourceId)

    @PutMapping("/sources/{sourceId}/verification", consumes = [MediaType.APPLICATION_JSON_VALUE])
    @ResponseStatus(HttpStatus.OK)
    @Operation(
        summary = "Turn the source's verification on or off",
        description = "Off: the cycle makes no job for it; an open job runs to its end.",
    )
    @NotFound
    @Unprocessable
    fun setSourceVerification(
        @PathVariable sourceId: UUID,
        @RequestBody input: SourceVerificationInput,
    ) = api.setSourceVerification(sourceId, input)

    @GetMapping("/sources/{sourceId}/verifications")
    @ResponseStatus(HttpStatus.OK)
    @Operation(summary = "The source's verifications", description = "Newest first.")
    @NotFound
    @Unprocessable
    fun listSourceVerifications(
        @PathVariable sourceId: UUID,
        @PageCursor @RequestParam(required = false) cursor: String?,
        @PageLimit @RequestParam(defaultValue = DEFAULT_LIMIT) limit: Int,
    ) = api.listSourceVerifications(sourceId, cursor, limit)

    @PostMapping("/sources/{sourceId}/verifications", consumes = [MediaType.APPLICATION_JSON_VALUE])
    @ResponseStatus(HttpStatus.ACCEPTED)
    @Operation(
        summary = "Verify now",
        description =
            "Queues a job for the given snapshot or the source's latest whole one; it waits for a fitting verifier " +
                "until the cycle's next moment. 409 verification_active while the source has an open job; 422 " +
                "snapshot_unusable for a snapshot that is not the source's or not whole.",
    )
    @NotFound
    @Unprocessable
    @VerificationActiveConflict
    fun verifyNow(
        @PathVariable sourceId: UUID,
        @RequestBody input: VerifyNowInput,
    ) = api.verifyNow(sourceId, input)

    @GetMapping("/verifications/{verificationId}")
    @ResponseStatus(HttpStatus.OK)
    @Operation(summary = "A verification with its stages")
    @NotFound
    fun getVerification(
        @PathVariable verificationId: UUID,
    ) = api.getVerification(verificationId)

    @GetMapping("/verification-schedule")
    @ResponseStatus(HttpStatus.OK)
    @Operation(summary = "The daily verification cycle of the installation")
    fun getVerificationSchedule() = api.getVerificationSchedule()

    @PutMapping("/verification-schedule", consumes = [MediaType.APPLICATION_JSON_VALUE])
    @ResponseStatus(HttpStatus.OK)
    @Operation(
        summary = "Set the daily verification cycle",
        description = "The change counts from now: a moment it skipped is not fired. 422 names cron or timezone.",
    )
    @Unprocessable
    fun setVerificationSchedule(
        @RequestBody input: VerificationScheduleInput,
    ) = api.setVerificationSchedule(input)

    @GetMapping("/verifiers")
    @ResponseStatus(HttpStatus.OK)
    @Operation(
        summary = "The installation's verifiers",
        description = "With sourceId, why each one would not take a job of the source's latest whole snapshot.",
    )
    @NotFound
    fun listVerifiers(
        @RequestParam(required = false) sourceId: UUID?,
    ) = api.listVerifiers(sourceId)
}
