// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import dev.sard.server.runs.StepOutput
import dev.sard.server.verify.FingerprintRow
import dev.sard.server.verify.MismatchWording
import dev.sard.server.verify.ReportEntry
import dev.sard.server.verify.VerificationHint
import dev.sard.server.verify.VerificationReport
import java.math.BigDecimal

// The V6 mappings of the verification endpoints: the report, the fingerprint, the hints.

internal fun reportOf(
    report: VerificationReport,
    serverVersion: String,
) = VerificationReportResponse(
    sourceId = report.sourceId,
    sourceName = report.sourceName,
    sourceDeleted = report.sourceDeleted,
    from = report.period.from,
    to = report.period.to,
    timezone = report.timezone,
    generatedAt = report.generatedAt,
    serverVersion = serverVersion,
    summary = ReportSummary(report.items.size, report.passed, report.failed, report.notHeld),
    items = report.items.map(::reportItemOf),
    failures = report.failures.map { ReportFailure(it.startedAt, it.records, it.fixedAt) },
)

internal fun reportItemOf(entry: ReportEntry) =
    ReportItem(
        entry.id,
        entry.verifiedAt,
        entry.snapshotId,
        entry.verifierHostname,
        outcomeOf(entry.status),
        entry.level,
        entry.durationMillis,
        entry.restoreMillis,
    )

/** A threshold in days that came as a number: whole, or 422 at its field. */
internal fun wholeDays(days: BigDecimal): Int =
    try {
        days.intValueExact()
    } catch (_: ArithmeticException) {
        throw RequestInvalid("staleAfterDays", "must be a whole number of days")
    }

internal fun hintOf(hint: VerificationHint): VerificationHintCode =
    when (hint) {
        VerificationHint.KEY_TABLES_MISSING -> VerificationHintCode.KEY_TABLES_MISSING
        VerificationHint.CONTROL_PATHS_MISSING -> VerificationHintCode.CONTROL_PATHS_MISSING
    }

internal fun mismatchOf(mismatch: StepOutput.Mismatch): FingerprintMismatch {
    val wording = MismatchWording.of(mismatch.name)
    return FingerprintMismatch(mismatch.name, wording.kind, wording.subject, mismatch.expected, mismatch.actual)
}

internal fun fieldOf(row: FingerprintRow) =
    FingerprintField(
        row.name,
        if (row.rule == "exact") FingerprintRule.EXACT else FingerprintRule.INFO,
        row.source,
        row.copy,
        FingerprintOutcomeKind.valueOf(row.outcome.name),
    )
