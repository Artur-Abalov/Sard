// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.registration

import dev.sard.server.persistence.Agent
import dev.sard.server.persistence.AgentPluginRecord
import dev.sard.server.persistence.AgentRepositoryRecord
import dev.sard.server.persistence.TenantSessions
import jakarta.persistence.LockModeType
import org.hibernate.Session
import java.time.Clock
import java.time.Instant
import java.util.UUID

private const val DELETE_PLUGINS = "delete from AgentPluginRecord where agentId = :agent"
private const val DELETE_REPOSITORIES = "delete from AgentRepositoryRecord where agentId = :agent"

/**
 * Register (S4a): replaces an agent's stored snapshot with the one it announced. The rules run
 * first, so a refused snapshot changes nothing. One transaction that starts by locking the
 * agent's row: a second Register of the same agent waits for the first to commit, then
 * replaces its snapshot whole; the two never mix.
 */
class Registration(
    private val sessions: TenantSessions,
    private val clock: Clock,
) {
    /** [tenantId] and [agentId] come from the authenticated certificate, never from the message. */
    fun register(
        tenantId: UUID,
        agentId: UUID,
        snapshot: AgentSnapshot,
    ) {
        SnapshotRules.check(snapshot)
        wrapUnexpected {
            sessions.inTenant(tenantId) { session -> replace(session, agentId, snapshot, clock.instant()) }
        }
    }

    private fun replace(
        session: Session,
        agentId: UUID,
        snapshot: AgentSnapshot,
        now: Instant,
    ) {
        val locked = session.find(Agent::class.java, agentId, LockModeType.PESSIMISTIC_WRITE)
        val agent = checkNotNull(locked) { "no agent $agentId" }
        agent.hostname = snapshot.hostname
        agent.agentVersion = snapshot.agentVersion
        agent.os = snapshot.os
        agent.arch = snapshot.arch
        agent.protocolVersion = snapshot.protocolVersion.toInt()
        agent.secretNames = snapshot.secretNames
        agent.scriptNames = snapshot.scriptNames
        agent.lastRegisterAt = now
        for (statement in listOf(DELETE_PLUGINS, DELETE_REPOSITORIES)) {
            session.createMutationQuery(statement).setParameter("agent", agentId).executeUpdate()
        }
        snapshot.plugins.forEach { session.persist(pluginRecord(agentId, it)) }
        snapshot.repositories.forEach { session.persist(repositoryRecord(agentId, it)) }
    }

    private fun pluginRecord(
        agentId: UUID,
        plugin: PluginEntry,
    ) = AgentPluginRecord(
        agentId,
        plugin.name,
        plugin.version,
        plugin.configSchema,
        // The rules have refused unknown actions already; nothing is dropped here.
        plugin.actions.filterNotNull().map { it.stored },
    )

    private fun repositoryRecord(
        agentId: UUID,
        repository: RepositoryEntry,
    ) = AgentRepositoryRecord(
        agentId,
        repository.name,
        repository.backend,
        repository.repositoryId.ifEmpty { null },
        repository.cryptoProvider.ifEmpty { null },
    )

    /** Whatever the database throws is the server's fault and retryable: INTERNAL_RETRYABLE (gRPC error model). */
    @Suppress("TooGenericExceptionCaught")
    private fun wrapUnexpected(block: () -> Unit) =
        try {
            block()
        } catch (e: Exception) {
            throw RegistrationRejectedException(RegistrationRejectedException.Reason.INTERNAL_RETRYABLE, cause = e)
        }
}
