// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.fleet

import dev.sard.server.api.MACHINE_ID
import dev.sard.server.api.OTHER_MACHINE_ID
import dev.sard.server.api.RestApiTest
import dev.sard.server.api.RestWorld
import dev.sard.server.api.snapshotOf
import dev.sard.server.enrollment.Enrollment
import dev.sard.server.enrollment.EnrollmentTokens
import dev.sard.server.pki.AgentRole
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
import kotlin.test.assertEquals

/** V1: "Агенты одной машины находятся по machine_id". */
@RestApiTest
class AgentsByMachineIdIntegrationTest(
    @Autowired ca: CertificateAuthority,
    @Autowired enrollment: Enrollment,
    @Autowired tokens: EnrollmentTokens,
    @Autowired registration: Registration,
    @Autowired jdbc: JdbcTemplate,
    @Autowired clock: MovableClock,
    @Autowired mapper: ObjectMapper,
    @Autowired private val agents: Agents,
    @Autowired private val machines: MachineAgents,
    @LocalServerPort port: Int,
    @Value("\${local.grpc.server.port}") grpc: Int,
) {
    private val world = RestWorld(ca, enrollment, tokens, registration, jdbc, clock, mapper, port, grpc)
    private val acme = world.tenant()
    private val globex = world.tenant()

    @AfterTest
    fun `drop the tenants`() = world.close()

    @Test
    fun `Агенты одной машины находятся по machine_id`() {
        val source = world.agent(acme, snapshotOf(machineId = MACHINE_ID), role = AgentRole.SOURCE)
        val verifier = world.agent(acme, snapshotOf(machineId = MACHINE_ID), role = AgentRole.VERIFIER)
        world.agent(acme, snapshotOf(machineId = OTHER_MACHINE_ID))
        world.agent(globex, snapshotOf(machineId = MACHINE_ID))
        world.enroll(acme)

        val found = machines.withMachineId(acme, MACHINE_ID)

        assertEquals(
            setOf(source.agentId to AgentRole.SOURCE, verifier.agentId to AgentRole.VERIFIER),
            found.map { it.id to it.role }.toSet(),
        )
        assertEquals(2, found.size)
    }

    @Test
    fun `Поиск по machine_id возвращает и отозванных агентов`() {
        val revoked = world.agent(acme, snapshotOf(machineId = MACHINE_ID))
        agents.revoke(acme, revoked.agentId, null)

        assertEquals(listOf(revoked.agentId), machines.withMachineId(acme, MACHINE_ID).map { it.id })
    }
}
