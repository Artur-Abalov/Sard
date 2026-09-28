// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.persistence

import jakarta.persistence.Column
import jakarta.persistence.Entity
import jakarta.persistence.Id
import jakarta.persistence.IdClass
import jakarta.persistence.Table
import org.hibernate.annotations.JdbcTypeCode
import org.hibernate.annotations.TenantId
import org.hibernate.type.SqlTypes
import java.io.Serializable
import java.util.UUID

/** The key of a row an agent owns by name: (agent_id, name). */
data class AgentOwnedKey(
    val agentId: UUID? = null,
    val name: String? = null,
) : Serializable {
    private companion object {
        private const val serialVersionUID = 1L
    }
}

/** A plugin the agent announced in its last Register (migration V202609281400). Replaced, never updated. */
@Entity
@Table(name = "agent_plugins")
@IdClass(AgentOwnedKey::class)
class AgentPluginRecord(
    @Id
    @Column(name = "agent_id")
    val agentId: UUID,
    @Id
    val name: String,
    @Column(nullable = false, updatable = false)
    val version: String,
    /** JSON text as the agent sent it; PostgreSQL stores it as jsonb. */
    @JdbcTypeCode(SqlTypes.JSON)
    @Column(name = "config_schema", nullable = false, updatable = false)
    val configSchema: String,
    /** lower_snake_case action names (ADR 0013, rule 7). */
    @JdbcTypeCode(SqlTypes.ARRAY)
    @Column(nullable = false, updatable = false)
    val actions: List<String>,
    @TenantId
    @Column(name = "tenant_id", nullable = false, updatable = false)
    val tenantId: UUID? = null,
)

/** A repository the agent announced in its last Register (migration V202609281400, ADR 0008). */
@Entity
@Table(name = "agent_repositories")
@IdClass(AgentOwnedKey::class)
class AgentRepositoryRecord(
    @Id
    @Column(name = "agent_id")
    val agentId: UUID,
    @Id
    val name: String,
    @Column(nullable = false, updatable = false)
    val backend: String,
    @Column(name = "repository_id", updatable = false)
    val repositoryId: String?,
    @Column(name = "crypto_provider", updatable = false)
    val cryptoProvider: String?,
    @TenantId
    @Column(name = "tenant_id", nullable = false, updatable = false)
    val tenantId: UUID? = null,
)
