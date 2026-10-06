// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.runs

import dev.sard.server.persistence.RunRecord
import dev.sard.server.persistence.RunStepRecord
import dev.sard.server.persistence.TenantSessions
import org.slf4j.LoggerFactory
import java.time.Instant
import java.util.UUID

private val log = LoggerFactory.getLogger(RunFinishedPublisher::class.java)

/**
 * A run reached its final state (ADR 0024: S7 publishes it, S9 notifies). Published after the
 * transaction that finished the run committed, once per run by the path whose guarded update
 * won: a StepResult or the lost window. [runId] identifies the event.
 */
data class RunFinished(
    val tenantId: UUID,
    val runId: UUID,
    val sourceId: UUID,
    val agentId: UUID,
    val trigger: Trigger,
    val status: RunState,
    /** Why it failed; null on success. */
    val message: String?,
    val finishedAt: Instant,
)

/** A subscriber to [RunFinished] (S9); called on the thread that finished the run, so it must be quick. */
fun interface RunFinishedListener {
    fun runFinished(event: RunFinished)
}

/**
 * Hands [RunFinished] to every listener. A failing listener is logged and skipped: the run is
 * already final and the agent's ResultAck must not wait on a subscriber. Delivery is at most
 * once for now (a crash between commit and publish loses the event); S9 needs at least once
 * (S7a, answer 4; ADR 0039).
 */
class RunFinishedPublisher(
    private val listeners: List<RunFinishedListener>,
) {
    fun publish(event: RunFinished) {
        for (listener in listeners) {
            runCatching { listener.runFinished(event) }.onFailure {
                log.error("RunFinished listener {} failed for run {}", listener.javaClass.name, event.runId, it)
            }
        }
    }

    companion object {
        val NONE = RunFinishedPublisher(emptyList())
    }
}

/** Reads the committed final run of a step and publishes its [RunFinished]; called after the commit. */
class RunAnnouncer(
    private val sessions: TenantSessions,
    private val publisher: RunFinishedPublisher,
) {
    fun announce(
        tenantId: UUID,
        stepId: UUID,
    ) {
        val event =
            sessions.inTenant(tenantId) { session ->
                val step = session.find(RunStepRecord::class.java, stepId)
                val run = session.find(RunRecord::class.java, step.runId)
                RunFinished(
                    tenantId,
                    run.id,
                    run.sourceId,
                    step.agentId,
                    Trigger.of(run.trigger),
                    RunState.of(run.status),
                    run.message,
                    checkNotNull(run.finishedAt) { "run ${run.id} is not finished" },
                )
            }
        publisher.publish(event)
    }
}
