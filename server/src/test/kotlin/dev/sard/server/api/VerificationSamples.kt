// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import java.time.Instant
import java.util.UUID

/** One sample of each schema of restore verification (V4, V6) for the serialization test: the contract has them all. */
object VerificationSamples {
    private val id = UUID.fromString("0192f7a0-0000-7000-8000-000000000001")
    private val at = Instant.parse("2026-09-27T10:00:00Z")

    private val verifierSample =
        Verifier(
            id,
            "verifier1",
            true,
            true,
            null,
            SandboxRuntime.DOCKER,
            "27.3.1",
            listOf("e"),
            listOf("files"),
            false,
            0,
            3,
            1,
            null,
        )
    private val reportItem =
        ReportItem(id, at, "4f3a9c1e", "vrf1", VerificationOutcome.PASSED, 5, 90_000, 252_000, null)
    private val attention = AttentionSource(id, "db-main", VerificationOutcome.FAILED, at, 5, null)
    private val verificationOverview = VerificationOverview(5, 1, 1, 2, 1, 1, 1, true, listOf(attention))
    private val verificationJob =
        VerificationJob(
            id,
            VerificationJobStatus.RUNNING,
            VerificationJobReason.CYCLE,
            at,
            at,
            1,
            emptyList(),
            Cancellation(at, "admin", CancelResult.PENDING),
        )
    private val verificationRecord =
        Verification(
            id,
            id,
            id,
            id,
            "4a3b2c1d",
            null,
            null,
            1,
            VerificationOutcome.NO_VERIFIER,
            0,
            "no verifier fits",
            listOf(VerificationStage(0, "fingerprint_tag", null, null, null, 0, "", "")),
            listOf(FingerprintMismatch("tables", FingerprintMismatchKind.TABLES, "", "12", "0")),
            listOf(VerifierMisfit(id, "verifier1", listOf(VerifierReason.OFFLINE))),
            null,
            null,
            false,
            at,
            listOf(FingerprintField("tables", FingerprintRule.EXACT, "12", "0", FingerprintOutcomeKind.MISMATCHED)),
            0,
            null,
            null,
        )

    val all: List<Any> =
        listOf(
            SourceVerificationInput(enabled = false),
            VerifyNowInput(snapshotId = id),
            VerificationScheduleInput("30 4 * * *", "UTC"),
            VerificationSchedule("30 4 * * *", "UTC", at, 2),
            VerifierMisfit(id, "verifier1", listOf(VerifierReason.SAME_HOST, VerifierReason.SPACE_UNKNOWN)),
            verificationJob,
            SourceVerificationState(
                true,
                VerificationOutcome.NO_VERIFIER,
                at,
                5,
                at,
                verificationJob,
                listOf(VerificationHintCode.KEY_TABLES_MISSING),
            ),
            VerificationStage(1, "restic_check", VerificationStageStatus.PASSED, at, at, 0, "ok", ""),
            FingerprintMismatch("tables", FingerprintMismatchKind.TABLES, "", "12", "0"),
            FingerprintField("tables", FingerprintRule.EXACT, "12", null, FingerprintOutcomeKind.NOT_COMPARED),
            VerificationSetup(
                AgentRole.VERIFIER,
                listOf(SetupRepository(id, "db1", "pg", "sudo sard-agent repo key add pg --for verifier")),
                "sudo sard-agent repo add <name> <address> --password-from-file <file>",
                "Read-only.",
            ),
            SetupRepository(id, "db1", "pg", null),
            VerificationReportResponse(
                id,
                "db-main",
                false,
                at,
                at,
                "UTC",
                at,
                "1.0.0",
                ReportSummary(4, 1, 1, 1, 1),
                listOf(reportItem),
                listOf(ReportFailure(at, 2, null)),
            ),
            ReportSummary(4, 1, 1, 1, 1),
            reportItem,
            ReportFailure(at, 1, at),
            verificationRecord,
            VerificationPage(listOf(verificationRecord), nextCursor = null),
            verifierSample,
            Overview(
                1,
                3,
                FirstSteps(true, true, false, false, false, complete = false),
                verificationOverview,
            ),
            verificationOverview,
            attention,
        )
}
