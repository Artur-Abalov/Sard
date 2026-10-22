// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.metrics

import dev.sard.server.role.AgentRole
import dev.sard.server.runs.RunState
import java.time.Instant
import java.util.UUID

/** The last success of a live source: null where it has none (the gauge then reads 0, owner's answer OV4). */
data class SourceSuccess(
    val sourceId: UUID,
    val lastBackup: Instant?,
    val lastVerification: Instant?,
)

/** The values of the label `health` of `sard.self.backup.health`, worst last as the console words them (МР12). */
val SELF_BACKUP_HEALTHS: List<String> =
    listOf("not_configured", "ok", "agent_replaced", "overdue", "failing", "schedule_disabled")

/** How the self-backup is doing and when it last succeeded; the time is null when it has had no success. */
data class SelfBackupMetric(
    val health: String,
    val lastSuccess: Instant?,
)

/** Where a tick asks about the self-backup: the package that knows it provides the answer (no cycle of packages). */
fun interface SelfBackupSource {
    fun current(): SelfBackupMetric
}

/**
 * The state of the installation as one tick read it (H1 phase 3). Maps hold the non-zero counts only; the gauges
 * add the zeros of a fixed set of labels (МР13).
 */
data class StateSnapshot(
    val sources: List<SourceSuccess>,
    val agents: Map<AgentRole, Long>,
    val agentsOnline: Map<AgentRole, Long>,
    val runsActive: Map<Pair<MetricRunKind, RunState>, Long>,
    val verificationJobs: Map<String, Long>,
    val undelivered: Map<String, Long>,
    /** The earliest end of a current agent certificate; null without one. */
    val nextCertificateExpiry: Instant?,
    val caExpiry: Instant,
    /** One of [SELF_BACKUP_HEALTHS]. */
    val selfBackupHealth: String,
    /** The newest success of a system source; null without one. */
    val selfBackupLastSuccess: Instant?,
)
