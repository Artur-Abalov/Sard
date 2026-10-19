// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.persistence

import jakarta.persistence.Column
import jakarta.persistence.Entity
import jakarta.persistence.Id
import jakarta.persistence.Table
import org.hibernate.annotations.TenantId
import java.time.Instant
import java.util.UUID

/** A certificate issued to an agent, keyed by its serial in lower-case hex (migration V202609271200, V202610151200). */
@Entity
@Table(name = "agent_certificates")
class AgentCertificateRecord(
    @Id
    val serial: String,
    @Column(name = "agent_id", nullable = false, updatable = false)
    val agentId: UUID,
    @Column(name = "issued_at", nullable = false, updatable = false)
    val issuedAt: Instant,
    @Column(name = "not_after", nullable = false, updatable = false)
    val notAfter: Instant,
    @Column(name = "revoked_at")
    var revokedAt: Instant? = null,
    /** The certificate of the same agent this one was issued against by a renewal (migration V202610151200). */
    @Column(name = "predecessor_serial", updatable = false)
    val predecessorSerial: String? = null,
    /** Set when a confirmed renewal, or a later one, replaced this certificate (migration V202610151200). */
    @Column(name = "superseded_at")
    var supersededAt: Instant? = null,
    /** Set by Hibernate from the session's tenant on insert (ADR 0013). */
    @TenantId
    @Column(name = "tenant_id", nullable = false, updatable = false)
    val tenantId: UUID? = null,
)
