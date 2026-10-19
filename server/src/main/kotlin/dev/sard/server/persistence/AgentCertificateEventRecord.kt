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

/**
 * A fact about an agent certificate that the periodic check recorded once (migration V202610191200): it reached a
 * threshold of its lifetime or expired. Rows are written by a guarded native statement in
 * `fleet/AgentCertificateCheck`, so the entity serves reads and the schema validation.
 */
@Entity
@Table(name = "agent_certificate_events")
class AgentCertificateEventRecord(
    @Id
    val id: UUID,
    @Column(nullable = false, updatable = false)
    val serial: String,
    @Column(name = "agent_id", nullable = false, updatable = false)
    val agentId: UUID,
    @Column(nullable = false, updatable = false)
    val event: String,
    @Column(name = "recorded_at", nullable = false, updatable = false)
    val recordedAt: Instant,
    @TenantId
    @Column(name = "tenant_id", nullable = false, updatable = false)
    val tenantId: UUID? = null,
)
