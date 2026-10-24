// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.metrics

import dev.sard.server.persistence.DatabaseMismatch
import dev.sard.server.persistence.DatabaseMismatchListener
import io.micrometer.core.instrument.Gauge
import io.micrometer.core.instrument.MeterRegistry

/**
 * `sard.database.settings.mismatch{reason}` (DB1): 1 for each way the server's database differs from a new
 * installation's, 0 for the others, as the start found it. Every reason is registered, so a difference that is not
 * there shows as zero.
 */
class DatabaseSettingsGauges(
    private val meters: MeterRegistry,
) : DatabaseMismatchListener {
    override fun found(mismatches: Set<DatabaseMismatch>) {
        for (mismatch in DatabaseMismatch.entries) {
            val value = if (mismatch in mismatches) 1.0 else 0.0
            Gauge
                .builder(SardMetricNames.DATABASE_SETTINGS_MISMATCH) { value }
                .tag("reason", mismatch.label)
                .register(meters)
        }
    }
}
