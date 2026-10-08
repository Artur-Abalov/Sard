// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.fleet

import dev.sard.server.api.RestApiTest
import dev.sard.server.api.RestWorld
import dev.sard.server.enrollment.Enrollment
import dev.sard.server.enrollment.EnrollmentTokens
import dev.sard.server.pki.CertificateAuthority
import dev.sard.server.pki.MovableClock
import dev.sard.server.registration.Registration
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.beans.factory.annotation.Value
import org.springframework.boot.test.web.server.LocalServerPort
import org.springframework.jdbc.core.JdbcTemplate
import tools.jackson.databind.ObjectMapper
import kotlin.test.AfterTest
import kotlin.test.Test
import kotlin.test.assertFalse
import kotlin.test.assertTrue

/** Rule "built-in and not revoked" of docs/specs/server/self-agent.feature, asked of the fleet. */
@RestApiTest
class AgentsBuiltinLiveIntegrationTest(
    @Autowired ca: CertificateAuthority,
    @Autowired enrollment: Enrollment,
    @Autowired tokens: EnrollmentTokens,
    @Autowired registration: Registration,
    @Autowired jdbc: JdbcTemplate,
    @Autowired clock: MovableClock,
    @Autowired mapper: ObjectMapper,
    @Autowired private val agents: Agents,
    @LocalServerPort port: Int,
    @Value("\${local.grpc.server.port}") grpc: Int,
) {
    private val world = RestWorld(ca, enrollment, tokens, registration, jdbc, clock, mapper, port, grpc)
    private val tenant = world.tenant()

    @AfterTest
    fun `drop the tenants`() = world.close()

    @Test
    fun `a tenant without a built-in agent has no live one even with ordinary agents`() {
        world.agent(tenant)

        assertFalse(agents.builtinLive(tenant))
    }

    @Test
    fun `a built-in agent is live until it is revoked`() {
        val builtin = world.agent(tenant, builtin = true)
        assertTrue(agents.builtinLive(tenant))

        agents.revoke(tenant, builtin.agentId, SELF_AGENT_CONFIRMATION)

        assertFalse(agents.builtinLive(tenant))
    }

    @Test
    fun `a built-in agent of another tenant is not seen`() {
        world.agent(world.tenant(), builtin = true)

        assertFalse(agents.builtinLive(tenant))
    }
}
