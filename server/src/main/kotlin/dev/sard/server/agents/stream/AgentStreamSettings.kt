// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents.stream

import org.springframework.boot.context.properties.ConfigurationProperties
import java.time.Duration

private const val DUPLICATE_WINDOW_HEARTBEATS = 2
private const val OFFLINE_AFTER_HEARTBEATS = 3
private const val SEND_QUEUE = 64
private val DEFAULT_PERIOD: Duration = Duration.ofSeconds(30)

/** The stream manager's timings, all derived in [AgentStreamProperties.settings]. */
data class AgentStreamSettings(
    /** `sard.agent.heartbeat-interval`: what Register tells the agent (S4a). */
    val heartbeatInterval: Duration,
    /** A session that got a message this recently is alive: a second stream is a duplicate. */
    val duplicateWindow: Duration,
    /** A session silent this long is closed and its agent is offline. */
    val offlineAfter: Duration,
    val helloTimeout: Duration,
    /** Messages waiting for one agent; a full queue refuses `send`. */
    val sendQueue: Int,
    /** How often sessions are swept and checked against the database. */
    val checkInterval: Duration,
    val clockSkewThreshold: Duration,
)

/** `sard.agent.stream.*`; windows are counted in heartbeat intervals (ADR 00XX-draft stream manager). */
@ConfigurationProperties("sard.agent.stream")
data class AgentStreamProperties(
    val duplicateWindowHeartbeats: Int = DUPLICATE_WINDOW_HEARTBEATS,
    val offlineAfterHeartbeats: Int = OFFLINE_AFTER_HEARTBEATS,
    val helloTimeout: Duration = DEFAULT_PERIOD,
    val sendQueue: Int = SEND_QUEUE,
    val checkInterval: Duration = DEFAULT_PERIOD,
    val clockSkewThreshold: Duration = DEFAULT_PERIOD,
) {
    fun settings(heartbeatInterval: Duration): AgentStreamSettings {
        validate(heartbeatInterval)
        return AgentStreamSettings(
            heartbeatInterval = heartbeatInterval,
            duplicateWindow = heartbeatInterval.multipliedBy(duplicateWindowHeartbeats.toLong()),
            offlineAfter = heartbeatInterval.multipliedBy(offlineAfterHeartbeats.toLong()),
            helloTimeout = helloTimeout,
            sendQueue = sendQueue,
            checkInterval = checkInterval,
            clockSkewThreshold = clockSkewThreshold,
        )
    }

    private fun validate(heartbeatInterval: Duration) {
        require(heartbeatInterval.isPositive) { "sard.agent.heartbeat-interval must be positive" }
        require(sendQueue > 0) { "sard.agent.stream.send-queue must be positive" }
        validateWindows()
    }

    /** A duplicate window of at least one interval, shorter than the offline one. */
    private fun validateWindows() {
        require(duplicateWindowHeartbeats >= 1) { "sard.agent.stream.duplicate-window-heartbeats must be at least 1" }
        require(duplicateWindowHeartbeats < offlineAfterHeartbeats) {
            "sard.agent.stream.duplicate-window-heartbeats must be below offline-after-heartbeats"
        }
    }
}
