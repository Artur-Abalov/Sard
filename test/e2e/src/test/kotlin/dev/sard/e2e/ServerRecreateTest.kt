// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.e2e

import org.junit.jupiter.api.extension.RegisterExtension
import java.sql.Timestamp
import java.time.Duration
import java.time.Instant
import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

/**
 * T3s, stand limit С1: the server's container is replaced by a new one, as an upgrade or a host
 * move does. Its CA and database are on volumes ([SardEnvironment.recreateServer]), so the new
 * server presents the same CA and the agent, which was never restarted nor enrolled again,
 * registers and connects to it with the certificate it already has.
 */
class ServerRecreateTest {
    @Test
    fun `a recreated server keeps its CA and the agent reconnects without enrolling again`() {
        val agent = AgentEnroller.enroll(sard, EnrollmentTokens.create(sard), hostname = "recreate")
        val container = sard.track("agent-recreate", AgentContainer.of(agent)).apply { start() }
        Await.until("Register of the agent") { Backups.connected(sard, agent.agentId) }
        val ca = ServerTls.fingerprint(ServerTls.presentedChain(sard).last())
        val agentStarted = container.currentContainerInfo.state.startedAt
        val oldServer = sard.server.containerId

        val recreatedAt = Instant.now()
        sard.recreateServer()

        assertTrue(sard.server.containerId != oldServer, "the server container was not replaced")
        assertEquals(ca, ServerTls.fingerprint(ServerTls.presentedChain(sard).last()), "the CA changed with the container")
        Await.until("Register and stream of the agent after the server came back", RECONNECT_TIMEOUT) {
            registeredAndSeenAfter(agent.agentId, recreatedAt)
        }
        assertEquals(listOf(agent.agentId), agentIds(), "the agent enrolled again")
        assertEquals(agentStarted, container.currentContainerInfo.state.startedAt, "the agent container restarted")
        assertTrue(container.isRunning, "the agent exited")
    }

    private fun registeredAndSeenAfter(
        agentId: String,
        instant: Instant,
    ): Boolean =
        sard.database().use { connection ->
            connection.prepareStatement("SELECT 1 FROM agents WHERE id = ? AND last_register_at > ? AND last_seen_at > ?").use { q ->
                q.setObject(1, UUID.fromString(agentId))
                q.setTimestamp(2, Timestamp.from(instant))
                q.setTimestamp(3, Timestamp.from(instant))
                q.executeQuery().use { it.next() }
            }
        }

    private fun agentIds(): List<String> =
        sard.database().use { connection ->
            connection.createStatement().use { q ->
                q.executeQuery("SELECT id FROM agents ORDER BY id").use { generateSequence { if (it.next()) it.getString(1) else null }.toList() }
            }
        }

    companion object {
        @JvmField
        @RegisterExtension
        val sard = SardEnvironment()

        /** The server's start plus the agent's reconnect backoff, capped at a minute (transport.go). */
        private val RECONNECT_TIMEOUT = Duration.ofMinutes(3)
    }
}
