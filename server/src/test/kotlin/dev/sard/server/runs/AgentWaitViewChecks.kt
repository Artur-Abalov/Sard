// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.runs

import io.github.anschnapp.mutflow.MutFlow
import org.springframework.jdbc.core.JdbcTemplate
import java.util.UUID
import kotlin.test.assertEquals
import kotlin.test.assertNotNull
import kotlin.test.assertNull

/**
 * J1c: what a run view says of its agent. A queued run asks whether its agent is online (none is, in these tests)
 * and shows the deadline of its wait; a run in flight does not ask and shows no deadline. [waiting] and [active] are
 * two sources of the tenant whose agent has no session. Shared by the mutation tests that read runs: only the reads
 * are under test, the list only if [listUnderTest] (it reaches the filter code).
 */
internal fun assertWaitingAndActiveViews(
    runs: Runs,
    jdbc: JdbcTemplate,
    tenantId: UUID,
    waiting: UUID,
    active: UUID,
    listUnderTest: Boolean = false,
) {
    val queued = runs.start(tenantId, waiting)
    val running = runs.start(tenantId, active)
    jdbc.update("update runs set status = 'running' where id = ?", running.id)
    val at = java.sql.Timestamp.from(RUNS_NOW)
    jdbc.update("update run_steps set status = 'running', dispatched_at = ? where run_id = ?", at, running.id)

    val views = MutFlow.underTest { listOf(runs.get(tenantId, queued.id), runs.get(tenantId, running.id)) }
    val all =
        if (listUnderTest) {
            MutFlow.underTest {
                runs.list(tenantId, RunFilter(), null, 50)
            }
        } else {
            runs.list(tenantId, RunFilter(), null, 50)
        }
    val listed = all.associateBy { it.id }

    for ((label, pair) in listOf("get" to views, "list" to listOf(listed[queued.id], listed[running.id]))) {
        val (waits, runs1) = pair
        assertNotNull(waits, label)
        assertNotNull(runs1, label)
        assertEquals(false, waits.agentOnline, "$label: a queued run's agent is offline")
        assertEquals(RunOutcome.WAITING_FOR_AGENT, waits.outcome, label)
        assertNotNull(waits.agentWaitUntil, label)
        assertEquals(true, runs1.agentOnline, "$label: a run in flight is not asked")
        assertEquals(RunOutcome.RUNNING, runs1.outcome, label)
        assertNull(runs1.agentWaitUntil, label)
    }
}
