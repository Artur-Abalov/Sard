// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.runs

import dev.sard.server.persistence.Agent
import dev.sard.server.persistence.AgentOwnedKey
import dev.sard.server.persistence.AgentPluginRecord
import dev.sard.server.persistence.PageKey
import dev.sard.server.persistence.SourceRecord
import dev.sard.server.persistence.TenantSessions
import dev.sard.server.persistence.UuidV7
import jakarta.persistence.LockModeType
import org.hibernate.Session
import org.hibernate.exception.ConstraintViolationException
import java.time.Clock
import java.util.UUID

private const val NAME_KEY = "sources_tenant_id_name_key"
private const val ACTIVE_RUN =
    "select id from RunRecord where sourceId = :source and status in ('queued', 'dispatched', 'running') " +
        "and trigger <> 'verification'"
private const val ACTIVE_RUN_OF_KIND = "$ACTIVE_RUN and kind = :kind"

/** The live source [id] of the session's tenant, locked with [lock]; a deleted one is absent. */
internal fun liveSource(
    session: Session,
    id: UUID,
    lock: LockModeType,
): SourceRecord {
    val record = session.find(SourceRecord::class.java, id, lock)
    return record?.takeIf { it.deletedAt == null } ?: throw SourceNotFound(id)
}

/** Refuses a source the server keeps itself (F6); its schedule is set through Schedules, not here. */
private fun SourceRecord.requireUserSource() {
    if (systemRole != null) throw SystemSourceProtected(id)
}

/**
 * The active run of [sourceId], if any. D6 holds for backups: at most one, a restore does not count ([kind] backup).
 * A source is deleted only without any active run, so a delete asks with no [kind].
 */
internal fun activeRunOf(
    session: Session,
    sourceId: UUID,
    kind: RunKind? = null,
): UUID? {
    val query =
        session
            .createSelectionQuery(if (kind == null) ACTIVE_RUN else ACTIVE_RUN_OF_KIND, UUID::class.java)
            .setParameter("source", sourceId)
    kind?.let { query.setParameter("kind", it.stored) }
    return query.resultList.firstOrNull()
}

/**
 * Sources of a tenant (S6a; S8b puts SourcesApi in front). The caller names the tenant it got
 * from the TenantResolver. A source is deleted softly and only without an active run; delete and
 * start both lock its row, so neither slips past the other.
 */
class Sources(
    private val sessions: TenantSessions,
    private val clock: Clock,
    private val ids: UuidV7,
) {
    fun create(
        tenantId: UUID,
        draft: SourceDraft,
    ): SourceView =
        nameGuarded(draft.name) {
            sessions.inTenant(tenantId) { session ->
                AgentOffer
                    .require(
                        session,
                        draft.agentId,
                        draft.plugin,
                        draft.repositoryName,
                        Action.BACKUP,
                        requireListed = true,
                    ).requireConfig(draft.config)
                val now = clock.instant()
                val record =
                    with(draft) { SourceRecord(ids.next(), agentId, name, plugin, config, repositoryName, now, now) }
                session.persist(record)
                session.flush()
                viewOf(session, record)
            }
        }

    /** Replaces the whole source (S8a: PUT); runs already started keep the config they were sent. */
    fun replace(
        tenantId: UUID,
        sourceId: UUID,
        draft: SourceDraft,
    ): SourceView =
        nameGuarded(draft.name) {
            sessions.inTenant(tenantId) { session ->
                val record = liveSource(session, sourceId, LockModeType.PESSIMISTIC_WRITE)
                record.requireUserSource()
                AgentOffer
                    .require(
                        session,
                        draft.agentId,
                        draft.plugin,
                        draft.repositoryName,
                        Action.BACKUP,
                        requireListed = true,
                    ).requireConfig(draft.config)
                record.name = draft.name
                record.agentId = draft.agentId
                record.plugin = draft.plugin
                record.repositoryName = draft.repositoryName
                record.config = draft.config
                record.updatedAt = clock.instant()
                session.flush()
                viewOf(session, record)
            }
        }

    /** Deletes softly; runs and snapshots of the source stay in the history (ADR 0013, rule 3). */
    fun delete(
        tenantId: UUID,
        sourceId: UUID,
    ) {
        sessions.inTenant(tenantId) { session ->
            val record = liveSource(session, sourceId, LockModeType.PESSIMISTIC_WRITE)
            record.requireUserSource()
            activeRunOf(session, sourceId)?.let { throw RunActive(it) }
            record.deletedAt = clock.instant()
        }
    }

    fun get(
        tenantId: UUID,
        sourceId: UUID,
    ): SourceView = sessions.inTenant(tenantId) { session -> viewOf(session, liveSource(session, sourceId, LockModeType.NONE)) }

    /** Live sources, newest first, after the position [after] if given, of [agentId] if given; at most [limit]. */
    fun list(
        tenantId: UUID,
        agentId: UUID?,
        after: PageKey?,
        limit: Int,
    ): List<SourceView> =
        sessions.inTenant(tenantId) { session ->
            val conditions =
                listOfNotNull(
                    "deletedAt is null",
                    agentId?.let { "agentId = :agent" },
                    after?.let { PageKey.condition("createdAt") },
                )
            val query =
                session.createSelectionQuery(
                    "from SourceRecord where ${conditions.joinToString(" and ")} order by createdAt desc, id desc",
                    SourceRecord::class.java,
                )
            agentId?.let { query.setParameter("agent", it) }
            after?.bind(query)
            query.setMaxResults(limit).list().map { viewOf(session, it) }
        }

    private fun <T> nameGuarded(
        name: String,
        block: () -> T,
    ): T =
        try {
            block()
        } catch (e: ConstraintViolationException) {
            if (e.constraintName == NAME_KEY) throw SourceNameTaken(name)
            throw e
        }

    private fun viewOf(
        session: Session,
        record: SourceRecord,
    ) = SourceView(
        record.id,
        record.name,
        record.agentId,
        record.plugin,
        record.repositoryName,
        record.config,
        record.createdAt,
        record.updatedAt,
        record.systemRole?.let(SystemRole::of),
        secretWarnings(session, record),
    )

    /** What the agent of [record] will refuse by the bindings it last reported (S11). */
    private fun secretWarnings(
        session: Session,
        record: SourceRecord,
    ): List<String> {
        val agent = session.find(Agent::class.java, record.agentId) ?: return emptyList()
        val plugin = session.find(AgentPluginRecord::class.java, AgentOwnedKey(record.agentId, record.plugin))
        return plugin?.let { SecretBindings.warnings(record.plugin, it.configSchema, record.config, agent.secretBindings) }.orEmpty()
    }
}
