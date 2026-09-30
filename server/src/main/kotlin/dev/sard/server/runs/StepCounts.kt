// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.runs

import dev.sard.server.persistence.TenantSessions

private const val WAITING =
    "select status, count(*) from RunStepRecord where status in ('queued', 'dispatched') group by status"

/** Steps waiting to reach an agent, across tenants: the dispatch metric (S6a). */
data class WaitingSteps(
    val queued: Long,
    val dispatched: Long,
)

/** The one system read of dispatch (ADR 0013, list of `system` callers): counts only, no rows. */
class StepCounts(
    private val sessions: TenantSessions,
) {
    fun waiting(): WaitingSteps =
        sessions.system { session ->
            val counts =
                session
                    .createSelectionQuery(WAITING, Array<Any>::class.java)
                    .list()
                    .associate { it[0] as String to it[1] as Long }
            WaitingSteps(counts[StepState.QUEUED.stored] ?: 0, counts[StepState.DISPATCHED.stored] ?: 0)
        }
}
