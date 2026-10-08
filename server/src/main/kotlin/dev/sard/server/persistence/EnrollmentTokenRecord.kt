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

/** A one-time enrollment token as stored: the hash of its secret, never the secret (migration V202609271200). */
@Entity
@Table(name = "enrollment_tokens")
class EnrollmentTokenRecord(
    @Id
    val id: UUID,
    @Column(name = "token_hash", nullable = false, updatable = false)
    val tokenHash: ByteArray,
    @Column(name = "expires_at", nullable = false, updatable = false)
    val expiresAt: Instant,
    @Column(name = "created_at", nullable = false, updatable = false)
    val createdAt: Instant,
    @Column(name = "used_at")
    var usedAt: Instant? = null,
    @Column(name = "agent_id")
    var agentId: UUID? = null,
    @Column(name = "revoked_at")
    var revokedAt: Instant? = null,
    /** Empty means no label (migration V202609271600, decision 2); never null. */
    @Column(name = "label", nullable = false)
    val label: String = "",
    /** Written by the server for the agent next to it (migration V202610081200); REST never shows it. */
    @Column(name = "builtin", nullable = false, updatable = false)
    val builtin: Boolean = false,
    /** Set by Hibernate from the session's tenant on insert (ADR 0013). */
    @TenantId
    @Column(name = "tenant_id", nullable = false, updatable = false)
    val tenantId: UUID? = null,
)
