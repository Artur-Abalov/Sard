// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.persistence

import jakarta.persistence.Column
import jakarta.persistence.Entity
import jakarta.persistence.Id
import jakarta.persistence.Table
import org.springframework.data.jpa.repository.JpaRepository
import java.time.Instant
import java.util.UUID

/** A registered agent; table created by Flyway migration V1. */
@Entity
@Table(name = "agents")
class Agent(
    @Id
    val id: UUID,
    @Column(nullable = false)
    val hostname: String,
    @Column(name = "agent_version", nullable = false)
    val agentVersion: String,
    @Column(name = "registered_at", nullable = false)
    val registeredAt: Instant,
    @Column(name = "last_seen_at")
    val lastSeenAt: Instant?,
)

/** Agents storage (roadmap: agent enrollment, stage 1). */
interface AgentRepository : JpaRepository<Agent, UUID>
