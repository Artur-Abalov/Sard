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
import java.math.BigDecimal
import java.time.Instant
import java.time.LocalDate
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

/** What a source's config lacks so that its verification proves more. */
@Schema(
    enumAsRef = true,
    description =
        "key_tables_missing: a PostgreSQL source without key tables is verified on its 10 largest tables; " +
            "control_paths_missing: a files source without control paths cannot tell an unmounted disk from a backup",
)
enum class VerificationHintCode {
    @JsonProperty("key_tables_missing")
    KEY_TABLES_MISSING,

    @JsonProperty("control_paths_missing")
    CONTROL_PATHS_MISSING,
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
    @field:Schema(description = "What the source's config lacks for a better verification; empty when nothing")
    val hints: List<VerificationHintCode>,
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
    @field:Schema(description = "The fact's raw name, such as rows:public.orders")
    val name: String,
    @field:Schema(description = "What the fact is about; the console and Telegram put it into words")
    val kind: FingerprintMismatchKind,
    @field:Schema(description = "A table, a path or an extension; empty for a fact about the whole copy")
    val subject: String,
    val expected: String,
    val actual: String,
)

/** What a differing fact of the fingerprint is about. */
@Schema(enumAsRef = true)
enum class FingerprintMismatchKind {
    @JsonProperty("rows")
    ROWS,

    @JsonProperty("tables")
    TABLES,

    @JsonProperty("extension")
    EXTENSION,

    @JsonProperty("extension_version")
    EXTENSION_VERSION,

    @JsonProperty("control_path")
    CONTROL_PATH,

    @JsonProperty("files")
    FILES,

    @JsonProperty("bytes")
    BYTES,

    @JsonProperty("file_hash")
    FILE_HASH,

    @JsonProperty("other")
    OTHER,
}

/** Whether a fingerprint fact is compared with the copy. */
@Schema(enumAsRef = true, description = "exact: compared with the copy; info: only shown")
enum class FingerprintRule {
    @JsonProperty("exact")
    EXACT,

    @JsonProperty("info")
    INFO,
}

/** How a fact of the fingerprint stands against the copy. */
@Schema(enumAsRef = true)
enum class FingerprintOutcomeKind {
    @JsonProperty("matched")
    MATCHED,

    @JsonProperty("mismatched")
    MISMATCHED,

    @JsonProperty("not_compared")
    NOT_COMPARED,
}

@Schema(description = "A field of the fingerprint: the source's value and the restored copy's, side by side")
data class FingerprintField(
    val name: String,
    val rule: FingerprintRule,
    val source: String,
    @field:Schema(description = "The copy's value; null when it is not compared or not known")
    val copy: String?,
    val outcome: FingerprintOutcomeKind,
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
    @field:Schema(description = "From the first stage's start to the last stage's finish; null without stage times")
    val durationMs: Long?,
    @field:Schema(description = "A pass right after a failure")
    val recovered: Boolean,
    val verifiedAt: Instant,
    @field:Schema(description = "The snapshot's source fingerprint beside the copy's, in the order of the backup")
    val fingerprint: List<FingerprintField>,
    @field:Schema(description = "Facts of the hashed sample of files: counted, never listed")
    val hiddenFacts: Int,
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
    @field:Schema(
        description =
            "Days without a successful verification after which a source is flagged, 1 to 90; null leaves it. " +
                "A fraction is refused, not rounded: it is read as it came.",
        types = ["integer"],
        minimum = "1",
        maximum = "90",
    )
    val staleAfterDays: BigDecimal? = null,
)

@Schema(description = "The daily verification cycle of the installation")
data class VerificationSchedule(
    val cron: String,
    val timezone: String,
    @field:Schema(description = "The cycle's next moment")
    val nextRunAt: Instant,
    @field:Schema(description = "Days without a successful verification after which a source is flagged")
    val staleAfterDays: Int,
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
    @field:Schema(description = "The runtime's version as the verifier reported it; null when unknown")
    val sandboxVersion: String?,
    @field:Schema(description = "restic ids of the repositories it holds a key to")
    val repositoryIds: List<String>,
    @field:Schema(description = "Plugins that verify")
    val verifyPlugins: List<String>,
    val busy: Boolean,
    @field:Schema(description = "Verifications handed to it in the last day")
    val recentVerifications: Int,
    @field:Schema(description = "Verifications it did in the last 30 days")
    val verificationsDone: Int,
    @field:Schema(description = "Of those, the ones that ended as its own infrastructure failure")
    val infrastructureFailures: Int,
    @field:Schema(description = "With sourceId: why it would not take the source's job now (empty: it would)")
    val reasons: List<VerifierReason>?,
)

@Schema(description = "A repository a source writes to: the key command to run on the source's host")
data class SetupRepository(
    val sourceAgentId: UUID,
    val sourceHostname: String,
    val repositoryName: String,
    @field:Schema(description = "Null when the repository name cannot be quoted for a shell")
    val keyAddCommand: String?,
)

@Schema(description = "How to get the first verifier going: commands to copy; nothing here is a secret")
data class VerificationSetup(
    @field:Schema(description = "The role of the enrollment token to issue for the verifier")
    val tokenRole: AgentRole,
    @field:Schema(description = "One entry per source agent and repository of the sources with verification on")
    val repositories: List<SetupRepository>,
    @field:Schema(description = "The command for the verifier's host; <name>, <address> and <file> are its own")
    val verifierRepoAdd: String,
    @field:Schema(description = "What to tell the administrator about the storage account of the verifier")
    val readOnlyNote: String,
)

@Schema(description = "Verifications of a period counted by outcome")
data class ReportSummary(
    val total: Int,
    val passed: Int,
    val failed: Int,
    @field:Schema(description = "Cannot verify, infrastructure failure or no fitting verifier: no check took place")
    val notHeld: Int,
)

@Schema(description = "A verification of the report: no evidence, no output")
data class ReportItem(
    val id: UUID,
    val verifiedAt: Instant,
    @field:Schema(description = "restic's id of the snapshot verified")
    val resticSnapshotId: String,
    @field:Schema(description = "The verifier's host; null when no verifier took part")
    val verifierHostname: String?,
    val status: VerificationOutcome,
    @field:Schema(description = LEVEL)
    val level: Int,
    @field:Schema(description = "From the first stage's start to the last stage's finish; null without times")
    val durationMs: Long?,
    @field:Schema(description = "The actual time to restore (level 5)")
    val restoreDurationMs: Long?,
)

@Schema(description = "A series of failed verifications and the pass that fixed it")
data class ReportFailure(
    @field:Schema(description = "The time of the first failure of the series")
    val startedAt: Instant,
    @field:Schema(description = "Failed verifications in the series")
    val records: Int,
    @field:Schema(description = "The pass that ended the series; null while not fixed")
    val fixedAt: Instant?,
)

@Schema(description = "A source's restore verifications for a period, for print")
data class VerificationReportResponse(
    val sourceId: UUID,
    val sourceName: String,
    @field:Schema(description = "The source is deleted; its history stays")
    val sourceDeleted: Boolean,
    @field:Schema(description = "The start of the period, included")
    val from: Instant,
    @field:Schema(description = "The end of the period, excluded")
    val to: Instant,
    @field:Schema(description = "The zone of the verification cycle, in which the dates of the request are days")
    val timezone: String,
    val generatedAt: Instant,
    @field:Schema(description = "The version of the server that made the report")
    val serverVersion: String,
    val summary: ReportSummary,
    @field:Schema(description = "Newest first")
    val items: List<ReportItem>,
    @field:Schema(description = "Series of failures that lasted into the period, oldest first")
    val failures: List<ReportFailure>,
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

    fun getVerificationSetup(): VerificationSetup

    fun getVerificationReport(
        sourceId: UUID,
        from: LocalDate?,
        to: LocalDate?,
    ): VerificationReportResponse
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
        description =
            "The change counts from now: a moment it skipped is not fired. 422 names cron, timezone or staleAfterDays.",
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

    @GetMapping("/verification-setup")
    @ResponseStatus(HttpStatus.OK)
    @Operation(
        summary = "How to set up the first verifier",
        description =
            "The commands for the steps of getting a verifier going: the token role, a key command per repository " +
                "of the sources, the repo add template for the verifier. No password passes through the console.",
    )
    fun getVerificationSetup() = api.getVerificationSetup()

    @GetMapping("/sources/{sourceId}/verification-report")
    @ResponseStatus(HttpStatus.OK)
    @Operation(
        summary = "Verification report of a source for a period",
        description =
            "from and to are dates, both included, in the zone of the verification cycle; without them the last " +
                "30 days up to now; at most 366 days. 422 names from or to. A deleted source keeps its report.",
    )
    @NotFound
    @Unprocessable
    fun getVerificationReport(
        @PathVariable sourceId: UUID,
        @RequestParam(required = false) from: LocalDate?,
        @RequestParam(required = false) to: LocalDate?,
    ) = api.getVerificationReport(sourceId, from, to)
}
