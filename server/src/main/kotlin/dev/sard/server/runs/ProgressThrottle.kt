// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.runs

import java.time.Clock
import java.time.Duration
import java.time.Instant
import java.util.UUID

/** Which progress messages reach the database. */
interface ProgressGate {
    /** True when this progress is to be written; it then counts as written now. */
    fun admit(
        agentId: UUID,
        stepId: UUID,
        phase: String?,
    ): Boolean

    /** The step is closed or unknown: its next message passes at once. */
    fun forget(
        agentId: UUID,
        stepId: UUID,
    )
}

/**
 * How often a step's progress reaches the database (S7a, answer 8; the agent does not thin it
 * yet, OQ-006): a new phase at once, the byte counters at most once per [interval]. Keyed by
 * agent and step, so another agent's messages never hold a step back. In memory, one node, like
 * the session registry (ADR 0026); a restart only lets one more write through.
 */
class ProgressThrottle(
    private val clock: Clock,
    private val interval: Duration,
    /** Beyond this many steps, entries older than [interval] are dropped before a new one is added. */
    private val capacity: Int,
) : ProgressGate {
    private data class Written(
        val at: Instant,
        val phase: String?,
    )

    private val written = HashMap<Pair<UUID, UUID>, Written>()

    /** True when this progress is to be written; it then counts as written now. */
    @Synchronized
    override fun admit(
        agentId: UUID,
        stepId: UUID,
        phase: String?,
    ): Boolean {
        val now = clock.instant()
        val key = agentId to stepId
        val last = written[key]
        val due = last == null || last.phase != phase || elapsed(last, now)
        if (due) {
            if (last == null) makeRoom(now)
            written[key] = Written(now, phase)
        }
        return due
    }

    private fun elapsed(
        last: Written,
        now: Instant,
    ) = !now.isBefore(last.at + interval)

    private fun makeRoom(now: Instant) {
        if (written.size >= capacity) written.values.removeIf { elapsed(it, now) }
    }

    /** The step is closed or unknown: its entry goes. */
    @Synchronized
    override fun forget(
        agentId: UUID,
        stepId: UUID,
    ) {
        written.remove(agentId to stepId)
    }

    @Synchronized
    fun size() = written.size
}
