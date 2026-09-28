// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents

import dev.sard.server.persistence.TenantSessions
import dev.sard.server.pki.AgentIdentity
import java.time.Instant
import java.util.UUID

private const val COLUMNS = "c.tenantId, c.agentId, c.notAfter, c.revokedAt, a.revokedAt"
private const val JOIN =
    "from AgentCertificateRecord c join Agent a on a.tenantId = c.tenantId and a.id = c.agentId "
private const val BY_SERIALS = "select c.serial, $COLUMNS $JOIN where c.serial in (:serials)"

private const val BY_SERIAL = "select $COLUMNS $JOIN where c.serial = :serial"

/**
 * The system lookups of certificates (ADR 0013): by serial on each agent call, before the tenant
 * is known (S3); by the serials of all open sessions on each stream check (S5a).
 */
class AgentCertificateStandings(
    private val sessions: TenantSessions,
) : CertificateStandings,
    BatchStandings {
    override fun of(serial: String): CertificateStanding? =
        sessions.system { session ->
            session
                .createSelectionQuery(BY_SERIAL, Array<Any?>::class.java)
                .setParameter("serial", serial)
                .uniqueResult()
                ?.let { row -> standing(row, 0) }
        }

    override fun of(serials: Collection<String>): Map<String, CertificateStanding> =
        sessions.system { session ->
            session
                .createSelectionQuery(BY_SERIALS, Array<Any?>::class.java)
                .setParameterList("serials", serials)
                .list()
                .associate { row -> row[0] as String to standing(row, 1) }
        }

    private fun standing(
        row: Array<Any?>,
        from: Int,
    ) = CertificateStanding(
        AgentIdentity(tenantId = row[from] as UUID, agentId = row[from + 1] as UUID),
        notAfter = row[from + 2] as Instant,
        revokedAt = row[from + 3] as Instant?,
        agentRevokedAt = row[from + 4] as Instant?,
    )
}
