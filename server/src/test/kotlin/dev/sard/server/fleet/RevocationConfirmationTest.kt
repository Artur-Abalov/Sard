// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.fleet

import dev.sard.server.persistence.Agent
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import java.time.Instant
import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertFalse
import kotlin.test.assertTrue

/**
 * Rule "Отзыв встроенного агента требует подтверждения" of docs/specs/server/self-agent.feature (О1: a revoked
 * one does not), decided apart from the revocation itself, whose stream closing mutflow cannot observe.
 */
@MutFlowTest
class RevocationConfirmationTest {
    private fun agent(
        builtin: Boolean,
        revokedAt: Instant? = null,
    ): Agent {
        val agent = Agent(UUID.randomUUID(), "sard-self", "1.0.0", Instant.EPOCH, lastSeenAt = null, builtin = builtin)
        agent.revokedAt = revokedAt
        return agent
    }

    @Test
    fun `a live built-in agent is refused without exactly the confirmation`() {
        val live = agent(builtin = true)
        for (confirmation in listOf(null, "", "sard-self ", "SARD-SELF", "yes")) {
            assertTrue(MutFlow.underTest { live.revocationUnconfirmed(confirmation) }, "$confirmation")
        }
    }

    @Test
    fun `a live built-in agent with the confirmation is revoked`() {
        assertFalse(MutFlow.underTest { agent(builtin = true).revocationUnconfirmed(SELF_AGENT_CONFIRMATION) })
    }

    @Test
    fun `a revoked built-in agent needs no confirmation`() {
        assertFalse(MutFlow.underTest { agent(builtin = true, revokedAt = Instant.EPOCH).revocationUnconfirmed(null) })
    }

    @Test
    fun `an ordinary agent needs no confirmation`() {
        assertFalse(MutFlow.underTest { agent(builtin = false).revocationUnconfirmed(null) })
        assertFalse(MutFlow.underTest { agent(builtin = false, revokedAt = Instant.EPOCH).revocationUnconfirmed(null) })
    }
}
