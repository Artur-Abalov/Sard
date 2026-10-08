// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents

import dev.sard.server.agents.AgentAuthResult.Rejected
import dev.sard.server.pki.PkiFixtures.AGENT
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

private const val METHOD = "sard.v1.AgentService/Connect"

@MutFlowTest
class AgentRefusalMessageTest {
    private fun message(rejected: Rejected) = MutFlow.underTest { refusalMessage(rejected, METHOD) }

    @Test
    fun `the log line of an unknown certificate names the tenant and explains the missing record`() {
        val line = message(Rejected(AgentAuthFailure.CERT_UNKNOWN, "8f0e", AGENT.agentId, AGENT.tenantId))
        val expected = "reason=CERT_UNKNOWN serial=8f0e agent=${AGENT.agentId} tenant=${AGENT.tenantId} method=$METHOD"
        assertTrue(expected in line, line)
        assertTrue("issued by this server's CA" in line, line)
        assertTrue("no record" in line, line)
        assertTrue("not restored or was restored from a copy older than the agent's enrollment" in line, line)
    }

    @Test
    fun `an unknown certificate that names no agent still explains itself`() {
        val line = message(Rejected(AgentAuthFailure.CERT_UNKNOWN, "8f0e", agentId = null, tenantId = null))
        assertTrue("agent=null tenant=null" in line, line)
        assertTrue("no record" in line, line)
    }

    @Test
    fun `every other refusal keeps the short line`() {
        for (failure in AgentAuthFailure.entries - AgentAuthFailure.CERT_UNKNOWN) {
            val line = message(Rejected(failure, "8f0e", AGENT.agentId))
            assertEquals("agent call refused: reason=$failure serial=8f0e agent=${AGENT.agentId} method=$METHOD", line)
            assertFalse("tenant" in line, line)
        }
    }
}
