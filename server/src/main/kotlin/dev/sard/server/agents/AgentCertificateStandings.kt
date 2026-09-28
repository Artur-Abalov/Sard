// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents

import dev.sard.server.persistence.TenantSessions
import dev.sard.server.pki.AgentIdentity
import java.time.Instant
import java.util.UUID

private const val BY_SERIAL =
    "select c.tenantId, c.agentId, c.notAfter, c.revokedAt, a.revokedAt " +
        "from AgentCertificateRecord c join Agent a on a.tenantId = c.tenantId and a.id = c.agentId " +
        "where c.serial = :serial"

/** The system lookup of S3: a certificate by serial, before the tenant is known (ADR 0013). */
class AgentCertificateStandings(
    private val sessions: TenantSessions,
) : CertificateStandings {
    override fun of(serial: String): CertificateStanding? =
        sessions.system { session ->
            session
                .createSelectionQuery(BY_SERIAL, Array<Any?>::class.java)
                .setParameter("serial", serial)
                .uniqueResult()
                ?.let { row ->
                    CertificateStanding(
                        AgentIdentity(tenantId = row[0] as UUID, agentId = row[1] as UUID),
                        notAfter = row[2] as Instant,
                        revokedAt = row[3] as Instant?,
                        agentRevokedAt = row[4] as Instant?,
                    )
                }
        }
}
