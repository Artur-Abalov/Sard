// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.fleet

import dev.sard.server.persistence.Agent
import dev.sard.server.persistence.TenantSessions
import java.util.UUID

private const val BY_MACHINE = "from Agent where machineId = :machine order by registeredAt, id"

/** Which agents share a host (V1): the signal for V4 that a source and a verifier stand on one machine. */
class MachineAgents(
    private val sessions: TenantSessions,
) {
    /**
     * Agents of [tenantId] whose last Register reported [machineId], revoked or not, of any role.
     * REST does not use it.
     */
    fun withMachineId(
        tenantId: UUID,
        machineId: String,
    ): List<AgentRow> =
        sessions.inTenant(tenantId) { session ->
            session
                .createSelectionQuery(BY_MACHINE, Agent::class.java)
                .setParameter("machine", machineId)
                .list()
                .map { it.toRow() }
        }
}
