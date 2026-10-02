// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.persistence

import jakarta.persistence.Column
import jakarta.persistence.Entity
import jakarta.persistence.Id
import jakarta.persistence.Table
import org.hibernate.annotations.JdbcTypeCode
import org.hibernate.annotations.TenantId
import org.hibernate.type.SqlTypes
import org.springframework.data.jpa.repository.JpaRepository
import java.time.Instant
import java.util.UUID

/**
 * A registered agent; table created by Flyway migrations V1, V2, V202609271200, V202609281200, V202609281400
 * and V202610011200.
 */
@Entity
@Table(name = "agents")
class Agent(
    @Id
    val id: UUID,
    @Column(nullable = false)
    var hostname: String,
    /** Null from Enroll until the agent's Register reports it. */
    @Column(name = "agent_version")
    var agentVersion: String?,
    @Column(name = "registered_at", nullable = false)
    val registeredAt: Instant,
    @Column(name = "last_seen_at")
    val lastSeenAt: Instant?,
    /** Set when the agent is revoked: none of its certificates authenticates any more (S3). */
    @Column(name = "revoked_at")
    var revokedAt: Instant? = null,
    /** Set by Hibernate from the current tenant on insert (ADR 0013). */
    @TenantId
    @Column(name = "tenant_id", nullable = false, updatable = false)
    val tenantId: UUID? = null,
) {
    // The snapshot of the last Register (S4a); null or empty before the first one.
    @Column
    var os: String? = null

    @Column
    var arch: String? = null

    @Column(name = "protocol_version")
    var protocolVersion: Int? = null

    @JdbcTypeCode(SqlTypes.ARRAY)
    @Column(name = "secret_names", nullable = false)
    var secretNames: List<String> = emptyList()

    @JdbcTypeCode(SqlTypes.ARRAY)
    @Column(name = "script_names", nullable = false)
    var scriptNames: List<String> = emptyList()

    @Column(name = "last_register_at")
    var lastRegisterAt: Instant? = null

    /** The last confirmed duplicate session (S8b, migration V202610011200); null if there never was one. */
    @Column(name = "duplicate_session_at")
    var duplicateSessionAt: Instant? = null
}

/** Agents storage (roadmap: agent enrollment, stage 1). */
interface AgentRepository : JpaRepository<Agent, UUID>
