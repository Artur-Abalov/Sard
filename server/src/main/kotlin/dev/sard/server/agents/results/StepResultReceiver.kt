// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents.results

import dev.sard.proto.agent.v1.StepResult
import dev.sard.server.agents.stream.ConnectedAgent
import dev.sard.server.agents.stream.SendResult
import dev.sard.server.agents.stream.StepResultHandler
import dev.sard.server.runs.Recorded
import dev.sard.server.runs.StepReport
import org.slf4j.LoggerFactory
import java.util.UUID

/** Where messages an agent should not have sent are logged: another agent's command, a malformed id. */
const val SECURITY_LOG = "dev.sard.server.security"

private val log = LoggerFactory.getLogger(StepResultReceiver::class.java)
private val security = LoggerFactory.getLogger(SECURITY_LOG)

/** Records a result; StepResults::record. Throws when nothing was recorded. */
fun interface ResultRecorder {
    fun record(
        tenantId: UUID,
        agentId: UUID,
        stepId: UUID,
        report: StepReport,
    ): Recorded
}

/** Sends ResultAck for [commandId] to the agent's session. */
fun interface ResultAcks {
    fun ack(
        agentId: UUID,
        commandId: String,
    ): SendResult
}

interface ResultMetrics {
    /** One result handled; [outcome] is the final status, or invalid, repeated, late, not_dispatched, unknown. */
    fun recorded(outcome: String)
}

/**
 * S5a's StepResult handler (S7a). ResultAck goes out only after [ResultRecorder] returned, that is
 * after its transaction committed; if recording throws, the exception ends the stream without an
 * ack and the agent sends the result again on its next stream, the only time it resends (proto
 * ResultAck). Every other case is acknowledged, a result for no step of this agent too, as the
 * proto asks: otherwise the agent would resend it forever.
 */
class StepResultReceiver(
    private val recorder: ResultRecorder,
    private val acks: ResultAcks,
    private val metrics: ResultMetrics,
) : StepResultHandler {
    override fun handle(
        agent: ConnectedAgent,
        result: StepResult,
    ) {
        val stepId = runCatching { UUID.fromString(result.commandId) }.getOrNull()
        val recorded =
            if (stepId == null) {
                security.warn(
                    "agent {} (tenant {}) sent a result for a command id that is no UUID ({} characters); " +
                        "acknowledged, nothing recorded",
                    agent.agentId,
                    agent.tenantId,
                    result.commandId.length,
                )
                Recorded.Unknown
            } else {
                recorder.record(agent.tenantId, agent.agentId, stepId, ProtoReports.of(result)).also {
                    report(agent, stepId, it)
                }
            }
        val sent = acks.ack(agent.agentId, result.commandId)
        if (sent != SendResult.Queued) {
            log.debug("ResultAck for {} not queued ({}); the agent resends on its next stream", stepId, sent)
        }
        metrics.recorded(outcome(recorded))
    }

    private fun report(
        agent: ConnectedAgent,
        stepId: UUID,
        recorded: Recorded,
    ) {
        when (recorded) {
            is Recorded.Closed -> {
                if (recorded.invalid != null) {
                    log.warn("agent {} sent an invalid result for step {}: {}", agent.agentId, stepId, recorded.invalid)
                }
            }

            is Recorded.Late -> {
                log.warn("result for lost step {}; it stays lost (output kept: {})", stepId, recorded.kept)
            }

            Recorded.NotDispatched -> {
                log.error("agent {} sent a result for step {}, never dispatched; not recorded", agent.agentId, stepId)
            }

            Recorded.Unknown -> {
                security.warn(
                    "agent {} (tenant {}) sent a result for command {}, which is no step of it; " +
                        "acknowledged, nothing recorded",
                    agent.agentId,
                    agent.tenantId,
                    stepId,
                )
            }

            Recorded.Repeated -> {
                log.debug("agent {} repeated the result of step {}", agent.agentId, stepId)
            }
        }
    }

    private fun outcome(recorded: Recorded): String =
        when (recorded) {
            is Recorded.Closed -> if (recorded.invalid != null) "invalid" else recorded.status.stored
            Recorded.Repeated -> "repeated"
            is Recorded.Late -> "late"
            Recorded.NotDispatched -> "not_dispatched"
            Recorded.Unknown -> "unknown"
        }
}
