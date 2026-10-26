// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.metrics

import dev.sard.server.persistence.RoleExcess
import dev.sard.server.persistence.RoleExcessListener
import io.micrometer.core.instrument.Gauge
import io.micrometer.core.instrument.MeterRegistry

/**
 * `sard.database.role.excess{reason}` (DB2): 1 for each thing the server's database role is that it needs not be,
 * 0 for the others, as the start found it. Every reason is registered, so a role without excess shows zeros.
 */
class RoleExcessGauges(
    private val meters: MeterRegistry,
) : RoleExcessListener {
    override fun found(excess: Set<RoleExcess>) {
        for (reason in RoleExcess.entries) {
            val value = if (reason in excess) 1.0 else 0.0
            Gauge
                .builder(SardMetricNames.DATABASE_ROLE_EXCESS) { value }
                .tag("reason", reason.label)
                .register(meters)
        }
    }
}
