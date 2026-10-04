// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.e2e

import org.junit.jupiter.api.extension.RegisterExtension
import java.time.Duration
import java.time.Instant
import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.fail

/**
 * The real sard-agent against the real server: after enrollment it calls
 * Register (S4a) and opens the Connect stream (S5a). The server's own record
 * is the evidence: `agents.last_register_at` (Register stored the snapshot)
 * and `last_seen_at` (a message arrived on the stream).
 *
 * The container and its tls files: [AgentContainer].
 */
class AgentConnectTest {
    @Test
    fun `an enrolled agent registers and connects its stream`() {
        val agent = AgentEnroller.enroll(sard, EnrollmentTokens.create(sard))
        sard.track(AgentContainer.ALIAS, AgentContainer.of(agent)).start()

        val row = awaitConnected(UUID.fromString(agent.agentId))
        assertEquals(E2e.version, row.version)
        assertEquals("linux", row.os)
    }

    private class AgentRow(
        val version: String?,
        val os: String?,
    )

    /** Polls the server's database until the agent has registered and been seen on its stream. */
    private fun awaitConnected(agentId: UUID): AgentRow {
        val deadline = Instant.now() + CONNECT_TIMEOUT
        while (Instant.now() < deadline) {
            connectedRow(agentId)?.let { return it }
            Thread.sleep(POLL.toMillis())
        }
        fail("agent $agentId did not register and connect within $CONNECT_TIMEOUT")
    }

    private fun connectedRow(agentId: UUID): AgentRow? =
        sard.database().use { connection ->
            connection
                .prepareStatement(
                    """
                    SELECT agent_version, os FROM agents
                    WHERE id = ? AND last_register_at IS NOT NULL AND last_seen_at IS NOT NULL
                    """.trimIndent(),
                ).use { query ->
                    query.setObject(1, agentId)
                    query.executeQuery().use { if (it.next()) AgentRow(it.getString(1), it.getString(2)) else null }
                }
        }

    companion object {
        @JvmField
        @RegisterExtension
        val sard = SardEnvironment()

        private val CONNECT_TIMEOUT = Duration.ofSeconds(60)
        private val POLL = Duration.ofMillis(500)
    }
}
