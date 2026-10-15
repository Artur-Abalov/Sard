// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents

import dev.sard.server.persistence.TenantSessions
import dev.sard.server.pki.AgentIdentity
import dev.sard.server.role.AgentRole
import java.time.Instant
import java.util.UUID

private const val COLUMNS =
    "c.tenantId, c.agentId, c.notAfter, c.revokedAt, a.revokedAt, a.role, c.supersededAt, c.predecessorSerial, p.supersededAt"
private const val JOIN =
    "from AgentCertificateRecord c join Agent a on a.tenantId = c.tenantId and a.id = c.agentId " +
        "left join AgentCertificateRecord p on p.serial = c.predecessorSerial "
private const val ROLE_COLUMN = 5
private const val SUPERSEDED_COLUMN = 6
private const val PREDECESSOR_COLUMN = 7
private const val PREDECESSOR_SUPERSEDED_COLUMN = 8
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
        role = AgentRole.of(row[from + ROLE_COLUMN] as String),
        supersededAt = row[from + SUPERSEDED_COLUMN] as Instant?,
        unconfirmed = unconfirmed(row, from),
    )

    /** Issued by a renewal that neither a confirmation nor a later renewal has superseded yet (C1, П7). */
    private fun unconfirmed(
        row: Array<Any?>,
        from: Int,
    ) = row[from + PREDECESSOR_COLUMN] != null &&
        row[from + SUPERSEDED_COLUMN] == null &&
        row[from + PREDECESSOR_SUPERSEDED_COLUMN] == null
}
