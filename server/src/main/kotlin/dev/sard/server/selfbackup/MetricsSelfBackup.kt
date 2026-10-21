// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.selfbackup

import dev.sard.server.metrics.SelfBackupMetric
import dev.sard.server.metrics.SelfBackupSource
import java.util.UUID

/**
 * What the state metrics ask about the self-backup of [tenantId] (H1 phase 3): its health, in the words of the label,
 * and the newest success of any of its system sources, which is none while the self-backup has had no success. Read in
 * a read-only transaction.
 */
class MetricsSelfBackup(
    private val selfBackups: SelfBackups,
    private val tenantId: UUID,
) : SelfBackupSource {
    override fun current(): SelfBackupMetric {
        val view = selfBackups.get(tenantId, readOnly = true)
        return SelfBackupMetric(view.health.name.lowercase(), view.sources.mapNotNull { it.lastSuccessAt }.maxOrNull())
    }
}
