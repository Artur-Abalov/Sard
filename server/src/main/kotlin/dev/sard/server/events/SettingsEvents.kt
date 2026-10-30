// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.events

import dev.sard.server.extension.SettingsChanged
import dev.sard.server.extension.TenantResolver
import java.time.Clock
import java.util.UUID

/**
 * Tells the listeners that a request changed the stored state (CORE1 Р20). Called after the change is committed
 * and only for a change that happened: [whenChanged] compares the state before and after. A failed request never
 * gets here; the event carries no value of the setting, so no secret.
 */
class SettingsEvents(
    private val publisher: SardEventPublisher,
    private val actors: ActorSource,
    private val clock: Clock,
    private val tenants: TenantResolver,
) {
    fun changed(
        setting: String,
        objectId: UUID? = null,
    ) = publisher.publish(SettingsChanged(clock.instant(), tenants.currentTenantId(), actors.current(), setting, objectId))

    /** Runs [action], and publishes when what [state] shows afterwards differs from what it showed before. */
    fun <T, S> whenChanged(
        setting: String,
        objectId: UUID?,
        state: () -> S,
        action: () -> T,
    ): T {
        val before = state()
        val result = action()
        if (state() != before) changed(setting, objectId)
        return result
    }

    companion object {
        /** Tells nobody; for the code that is built without a context. */
        val NONE =
            SettingsEvents(
                SardEventPublisher.NONE,
                ActorSource.NONE,
                Clock.systemUTC(),
                TenantResolver { TenantResolver.DEFAULT_TENANT_ID },
            )
    }
}
