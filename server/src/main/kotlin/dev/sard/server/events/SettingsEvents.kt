// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.events

import dev.sard.server.extension.SettingsChanged
import dev.sard.server.extension.TenantResolver
import org.hibernate.Session
import java.time.Clock
import java.util.UUID

/**
 * Tells the listeners that a request changed the stored state (CORE1 Р20). The code that writes a setting decides
 * whether it changed, in the transaction of the write, and asks for the event there ([changed] with a session,
 * Р28); the event goes out once that transaction has committed. The event carries no value of the setting, so no
 * secret.
 */
class SettingsEvents(
    private val publisher: SardEventPublisher,
    private val actors: SubjectActors,
    private val clock: Clock,
    private val tenants: TenantResolver,
) {
    /**
     * Publishes now, for a change that one statement already decided and committed (the administrator's password:
     * its insert and its compare-and-set update commit by themselves).
     */
    fun changed(
        setting: String,
        objectId: UUID? = null,
    ) {
        val event = SettingsChanged(clock.instant(), tenants.currentTenantId(), actors.current(), setting, objectId)
        publisher.publish(event)
    }

    /**
     * The write in [session]'s transaction changed [setting] (CORE1 Р28): the caller has decided that under the
     * lock of the write, and the listeners hear of it once the transaction has committed, never if it rolls back.
     */
    fun changed(
        session: Session,
        tenantId: UUID,
        setting: String,
        objectId: UUID? = null,
    ) {
        val event = SettingsChanged(clock.instant(), tenantId, actors.current(), setting, objectId)
        session.transaction.registerSynchronization(AfterCommit { publisher.publish(event) })
    }

    /** As [changed] when a statement of the write changed [rows] rows: none, no event. */
    fun changedRows(
        rows: Int,
        session: Session,
        tenantId: UUID,
        setting: String,
        objectId: UUID? = null,
    ) {
        if (rows > 0) changed(session, tenantId, setting, objectId)
    }

    /**
     * As [changed] when the write left the setting as `states.second` where `states.first` was read under its lock.
     * Both are values with the equality of the setting: what is not part of it (a time stamp) is not in them.
     */
    fun <T> changedIf(
        states: Pair<T, T>,
        session: Session,
        tenantId: UUID,
        setting: String,
        objectId: UUID? = null,
    ) {
        if (states.first != states.second) changed(session, tenantId, setting, objectId)
    }

    companion object {
        /** Tells nobody; for the code that is built without a context. */
        val NONE =
            SettingsEvents(
                SardEventPublisher.NONE,
                SubjectActors.NONE,
                Clock.systemUTC(),
                TenantResolver { TenantResolver.DEFAULT_TENANT_ID },
            )
    }
}
