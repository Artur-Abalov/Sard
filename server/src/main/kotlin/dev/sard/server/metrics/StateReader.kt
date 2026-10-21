// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.metrics

import dev.sard.server.role.AgentRole
import dev.sard.server.runs.RunState
import java.time.Clock
import java.time.Duration
import java.time.Instant
import java.util.UUID

/** How far back a delivery that did not reach its channel still counts (owner's answer OV5). */
private val UNDELIVERED_WINDOW: Duration = Duration.ofHours(24)

/** What the database tells a tick, summed over every tenant; the memory of the server is added by [StateReader]. */
class StateRows(
    val sources: List<SourceSuccess>,
    /** Every agent that is not revoked, with its role. */
    val agents: List<Pair<AgentRole, UUID>>,
    val runsActive: Map<Pair<MetricRunKind, RunState>, Long>,
    val verificationJobs: Map<String, Long>,
    val undelivered: Map<String, Long>,
    val nextCertificateExpiry: Instant?,
)

/**
 * Puts the [StateSnapshot] of a tick together: the [rows] of the database (given the start of the window of
 * undelivered notifications), the agents [online] now (the registry of streams, not the database), the end of the CA
 * and the standing of the self-backup.
 */
class StateReader(
    private val rows: (since: Instant) -> StateRows,
    private val online: () -> Set<UUID>,
    private val caExpiry: () -> Instant,
    private val selfBackup: SelfBackupSource,
    private val clock: Clock,
) {
    fun read(): StateSnapshot {
        val database = rows(clock.instant().minus(UNDELIVERED_WINDOW))
        val connected = online()
        val standing = selfBackup.current()
        return StateSnapshot(
            sources = database.sources,
            agents =
                database.agents
                    .groupingBy { it.first }
                    .eachCount()
                    .mapValues { it.value.toLong() },
            agentsOnline =
                database.agents
                    .filter { it.second in connected }
                    .groupingBy { it.first }
                    .eachCount()
                    .mapValues { it.value.toLong() },
            runsActive = database.runsActive,
            verificationJobs = database.verificationJobs,
            undelivered = database.undelivered,
            nextCertificateExpiry = database.nextCertificateExpiry,
            caExpiry = caExpiry(),
            selfBackupHealth = standing.health,
            selfBackupLastSuccess = standing.lastSuccess,
        )
    }
}
