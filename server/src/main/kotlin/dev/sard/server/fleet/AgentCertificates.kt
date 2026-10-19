// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.fleet

import dev.sard.server.persistence.TenantSessions
import org.hibernate.Session
import java.time.Instant
import java.util.UUID

/**
 * The current certificate of a not revoked agent (C1 phase 3, П32): not revoked, not superseded, and confirmed, that
 * is without a predecessor or with a predecessor that a confirmation superseded. Of several such records (a
 * database edit) the one issued last. [renewalIssuedAt] is when a renewal of it was issued that nobody confirmed.
 */
data class CurrentCertificate(
    val serial: String,
    val lifetime: AgentCertificateLifetime,
    val renewalIssuedAt: Instant?,
)

/** A row of [CURRENT]: the certificate, newest first. */
data class CertificateRow(
    val agentId: UUID,
    val serial: String,
    val issuedAt: Instant,
    val notAfter: Instant,
)

/** A row of [UNCONFIRMED]: when the latest unconfirmed renewal of [serial] was issued. */
data class UnconfirmedRow(
    val serial: String,
    val issuedAt: Instant,
)

private const val CURRENT = """
    select new dev.sard.server.fleet.CertificateRow(c.agentId, c.serial, c.issuedAt, c.notAfter)
    from AgentCertificateRecord c
    join Agent a on a.tenantId = c.tenantId and a.id = c.agentId
    left join AgentCertificateRecord p on p.tenantId = c.tenantId and p.serial = c.predecessorSerial
    where c.agentId in :agents and a.revokedAt is null and c.revokedAt is null and c.supersededAt is null
      and (c.predecessorSerial is null or p.supersededAt is not null)
    order by c.issuedAt desc, c.serial desc"""

private const val UNCONFIRMED = """
    select new dev.sard.server.fleet.UnconfirmedRow(s.predecessorSerial, max(s.issuedAt))
    from AgentCertificateRecord s
    where s.predecessorSerial in :serials and s.supersededAt is null and s.revokedAt is null
    group by s.predecessorSerial"""

/** The certificates of the agents of a tenant as the console shows them (C1 phase 3, П33). */
class AgentCertificates(
    private val sessions: TenantSessions,
) {
    /** The current certificate of each of [agentIds] that has one; a revoked agent or one without is absent. */
    fun current(
        tenantId: UUID,
        agentIds: Collection<UUID>,
    ): Map<UUID, CurrentCertificate> =
        if (agentIds.isEmpty()) emptyMap() else sessions.inTenant(tenantId) { currentCertificates(it, agentIds) }
}

/** [AgentCertificates.current] in the session of the tenant of [agentIds]. */
internal fun currentCertificates(
    session: Session,
    agentIds: Collection<UUID>,
): Map<UUID, CurrentCertificate> {
    val rows =
        session
            .createSelectionQuery(CURRENT, CertificateRow::class.java)
            .setParameterList("agents", agentIds)
            .list()
    // Newest first: the first row of an agent is its current certificate.
    val newest = rows.groupBy { it.agentId }.mapValues { (_, own) -> own.first() }
    val unconfirmed = unconfirmedRenewals(session, newest.values.map { it.serial })
    return newest.mapValues { (_, row) ->
        CurrentCertificate(row.serial, AgentCertificateLifetime(row.issuedAt, row.notAfter), unconfirmed[row.serial])
    }
}

/** For each of [serials], when its latest renewal that no one confirmed was issued. */
private fun unconfirmedRenewals(
    session: Session,
    serials: Collection<String>,
): Map<String, Instant> {
    if (serials.isEmpty()) return emptyMap()
    return session
        .createSelectionQuery(UNCONFIRMED, UnconfirmedRow::class.java)
        .setParameterList("serials", serials)
        .list()
        .associate { it.serial to it.issuedAt }
}
