// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.fleet

import dev.sard.server.agents.dispatch.ReconciledHellos
import dev.sard.server.agents.stream.Ended
import dev.sard.server.api.RestApiTest
import dev.sard.server.api.RestWorld
import dev.sard.server.enrollment.Enrollment
import dev.sard.server.enrollment.EnrollmentTokens
import dev.sard.server.pki.CertificateAuthority
import dev.sard.server.pki.MovableClock
import dev.sard.server.registration.Registration
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import io.grpc.Status
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.beans.factory.annotation.Value
import org.springframework.boot.test.web.server.LocalServerPort
import org.springframework.jdbc.core.JdbcTemplate
import tools.jackson.databind.ObjectMapper
import java.util.UUID
import kotlin.test.AfterTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertIs
import kotlin.test.assertNotNull
import kotlin.test.assertTrue

/** Rule "built-in and not revoked" of docs/specs/server/self-agent.feature, asked of the fleet. */
@MutFlowTest
@RestApiTest
class AgentsBuiltinLiveIntegrationTest(
    @Autowired ca: CertificateAuthority,
    @Autowired enrollment: Enrollment,
    @Autowired tokens: EnrollmentTokens,
    @Autowired registration: Registration,
    @Autowired private val jdbc: JdbcTemplate,
    @Autowired private val hellos: ReconciledHellos,
    @Autowired clock: MovableClock,
    @Autowired mapper: ObjectMapper,
    @Autowired private val agents: Agents,
    @LocalServerPort port: Int,
    @Value("\${local.grpc.server.port}") grpc: Int,
) {
    private val world = RestWorld(ca, enrollment, tokens, registration, jdbc, clock, mapper, port, grpc)
    private val tenant = world.tenant()

    private fun builtinLive() = MutFlow.underTest { agents.builtinLive(tenant) }

    // Not under mutflow: whether a revocation needs the confirmation is RevocationConfirmationTest's, and a
    // revocation closes the stream asynchronously, past what a mutant run can observe.
    private fun revoke(
        agent: UUID,
        confirmation: String?,
    ) = agents.revoke(tenant, agent, confirmation)

    private fun revokedAt(agent: UUID) = MutFlow.underTest { agents.get(tenant, agent) }?.row?.revokedAt

    @AfterTest
    fun `drop the tenants`() = world.close()

    @Test
    fun `a tenant without a built-in agent has no live one even with ordinary agents`() {
        world.agent(tenant)

        assertFalse(builtinLive())
    }

    @Test
    fun `a built-in agent is live until it is revoked`() {
        val builtin = world.agent(tenant, builtin = true)
        assertTrue(builtinLive())

        agents.revoke(tenant, builtin.agentId, SELF_AGENT_CONFIRMATION)

        assertFalse(builtinLive())
    }

    @Test
    fun `a built-in agent of another tenant is not seen`() {
        world.agent(world.tenant(), builtin = true)

        assertFalse(builtinLive())
    }

    @Test
    fun `a live built-in agent is revoked only with exactly the confirmation`() {
        val builtin = world.agent(tenant, builtin = true)

        for (confirmation in listOf(null, "", "sard-self ", "SARD-SELF", "yes")) {
            assertEquals(AgentRevocation.ConfirmationRequired, revoke(builtin.agentId, confirmation), "$confirmation")
            assertEquals(null, revokedAt(builtin.agentId), "$confirmation")
        }
        assertTrue(builtinLive())

        val revoked = assertIs<AgentRevocation.Revoked>(agents.revoke(tenant, builtin.agentId, SELF_AGENT_CONFIRMATION))
        assertNotNull(revoked.card.row.revokedAt)
        assertFalse(builtinLive())
    }

    @Test
    fun `an ordinary agent is revoked without a confirmation`() {
        val ordinary = world.agent(tenant)

        assertIs<AgentRevocation.Revoked>(agents.revoke(tenant, ordinary.agentId, null))
        assertNotNull(revokedAt(ordinary.agentId))
    }

    @Test
    fun `a revoked built-in agent needs no confirmation, its stream is closed, an unknown agent is not found`() {
        val builtin = world.agent(tenant, builtin = true)
        val connection = world.connect(builtin)
        connection.hello()
        hellos.await(builtin.agentId)
        jdbc.update("update agents set revoked_at = now() where id = ?", builtin.agentId)

        val again = assertIs<AgentRevocation.Revoked>(revoke(builtin.agentId, null))

        assertNotNull(again.card.row.revokedAt)
        assertEquals(Ended(Status.Code.UNAUTHENTICATED, "AGENT_REVOKED"), connection.ended())
        assertEquals(AgentRevocation.NotFound, revoke(UUID.randomUUID(), SELF_AGENT_CONFIRMATION))
    }

    @Test
    fun `the card tells a built-in agent from an ordinary one`() {
        val builtin = world.agent(tenant, builtin = true)
        val ordinary = world.agent(tenant)

        assertEquals(true, MutFlow.underTest { agents.get(tenant, builtin.agentId) }?.row?.builtin)
        assertEquals(false, MutFlow.underTest { agents.get(tenant, ordinary.agentId) }?.row?.builtin)
    }
}
