// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.fleet

import dev.sard.server.persistence.TenantSessions
import dev.sard.server.persistence.UuidV7
import org.hibernate.Session
import org.slf4j.LoggerFactory
import java.time.Clock
import java.time.Instant
import java.util.UUID

private val log = LoggerFactory.getLogger(AgentCertificateCheck::class.java)

// The current certificate of every agent that is not revoked (C1 phase 3, П32), with the events recorded about it.
private const val SCAN = """
    select new dev.sard.server.fleet.ScanRow(c.tenantId, c.agentId, c.serial, c.issuedAt, c.notAfter, e.event)
    from AgentCertificateRecord c
    join Agent a on a.tenantId = c.tenantId and a.id = c.agentId
    left join AgentCertificateRecord p on p.tenantId = c.tenantId and p.serial = c.predecessorSerial
    left join AgentCertificateEventRecord e on e.tenantId = c.tenantId and e.serial = c.serial
    where a.revokedAt is null and c.revokedAt is null and c.supersededAt is null
      and (c.predecessorSerial is null or p.supersededAt is not null)
    order by c.issuedAt desc, c.serial desc"""

// Native SQL names tenant_id explicitly (ADR 0013, rule 8). The insert is guarded: only a certificate that is still
// current of an agent that is not revoked gets the event, and the key (tenant, serial, event) admits it once.
private const val RECORD = """
    insert into agent_certificate_events (id, tenant_id, serial, agent_id, event, recorded_at)
    select :id, c.tenant_id, c.serial, c.agent_id, :event, :now
    from agent_certificates c
    join agents a on a.tenant_id = c.tenant_id and a.id = c.agent_id
    where c.tenant_id = :tenant and c.serial = :serial and c.revoked_at is null and c.superseded_at is null
      and a.revoked_at is null
    on conflict (tenant_id, serial, event) do nothing"""

/** A row of [SCAN]: a current certificate and one event recorded about it, if any. */
data class ScanRow(
    val tenantId: UUID?,
    val agentId: UUID,
    val serial: String,
    val issuedAt: Instant,
    val notAfter: Instant,
    val event: String?,
)

/** The current certificate of an agent as the scan found it, with the events recorded about it already. */
private class Scanned(
    val tenantId: UUID,
    val agentId: UUID,
    val serial: String,
    val lifetime: AgentCertificateLifetime,
    val recorded: Set<CertificateEvent>,
)

/**
 * Records, once per certificate, that the current certificate of an agent reached a threshold of its lifetime or
 * expired (C1 phase 3, П35 and П36). The scan reads across tenants in the read-only system session (ADR 0013); every
 * write runs in the certificate's own tenant and is guarded by the key of the event, so a second server, or a
 * repeated check, records nothing twice. Notifying is the queue's work (S9a), not this class's.
 */
class AgentCertificateCheck(
    private val sessions: TenantSessions,
    private val clock: Clock,
    private val ids: UuidV7,
) {
    fun tick() {
        val now = clock.instant()
        for (certificate in sessions.system { scan(it) }) {
            CertificateEvent.due(certificate.lifetime, now, certificate.recorded)?.let { record(certificate, it, now) }
        }
    }

    private fun scan(session: Session): List<Scanned> =
        session
            .createSelectionQuery(SCAN, ScanRow::class.java)
            .list()
            .groupBy { it.tenantId to it.agentId }
            .values
            .map(::newest)
            .filter { CertificateEvent.EXPIRED !in it.recorded }

    /** The rows of one agent, newest certificate first: the first certificate with all the events it has. */
    private fun newest(rows: List<ScanRow>): Scanned {
        val first = rows.first()
        return Scanned(
            tenantId = checkNotNull(first.tenantId),
            agentId = first.agentId,
            serial = first.serial,
            lifetime = AgentCertificateLifetime(first.issuedAt, first.notAfter),
            recorded =
                rows
                    .filter { it.serial == first.serial }
                    .mapNotNull { it.event }
                    .map(CertificateEvent::of)
                    .toSet(),
        )
    }

    private fun record(
        certificate: Scanned,
        event: CertificateEvent,
        now: Instant,
    ) {
        val written =
            sessions.inTenant(certificate.tenantId) { session ->
                session
                    .createNativeMutationQuery(RECORD)
                    .setParameter("id", ids.next())
                    .setParameter("tenant", certificate.tenantId)
                    .setParameter("serial", certificate.serial)
                    .setParameter("event", event.wire)
                    .setParameter("now", now)
                    .executeUpdate()
            }
        if (written == 1) logRecorded(certificate, event)
    }

    private fun logRecorded(
        certificate: Scanned,
        event: CertificateEvent,
    ) {
        when (event) {
            CertificateEvent.EXPIRED -> {
                log.info("Agent certificate {} of agent {} expired", certificate.serial, certificate.agentId)
            }

            CertificateEvent.THRESHOLD_30, CertificateEvent.THRESHOLD_7, CertificateEvent.THRESHOLD_1 -> {
                val line = "Agent certificate {} of agent {} reached {}/365 of its lifetime left"
                log.info(line, certificate.serial, certificate.agentId, event.days)
            }
        }
    }
}
