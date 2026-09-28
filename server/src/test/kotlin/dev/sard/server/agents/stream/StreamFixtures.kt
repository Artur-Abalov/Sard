// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents.stream

import java.time.Duration
import java.time.Instant
import java.util.UUID

/** Unit-test values of the stream manager: round numbers so boundaries read at a glance. */
object StreamFixtures {
    val NOW: Instant = Instant.parse("2026-09-28T10:00:00Z")
    val HEARTBEAT: Duration = Duration.ofSeconds(30)

    val SETTINGS =
        AgentStreamSettings(
            heartbeatInterval = HEARTBEAT,
            duplicateWindow = HEARTBEAT.multipliedBy(2),
            offlineAfter = HEARTBEAT.multipliedBy(3),
            helloTimeout = HEARTBEAT,
            sendQueue = 4,
            checkInterval = HEARTBEAT,
            clockSkewThreshold = Duration.ofSeconds(10),
        )

    fun agent(
        agentId: UUID = UUID.randomUUID(),
        tenantId: UUID = UUID.fromString("7f3c1a52-0b4e-4c1d-9a55-2d8e6f0b9c11"),
    ) = ConnectedAgent(agentId, tenantId, serial = "8f0e5c2a9b7d4e6f8a1b2c3d4e5f6a7b")

    fun stream(
        agent: ConnectedAgent = agent(),
        openedAt: Instant = NOW,
    ) = AgentStream(agent, openedAt, SETTINGS.sendQueue)
}
