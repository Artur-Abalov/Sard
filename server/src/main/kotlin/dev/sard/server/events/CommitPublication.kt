// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.events

import dev.sard.server.extension.SardEvent
import jakarta.transaction.Status
import jakarta.transaction.Synchronization

/**
 * Publishes [event] when the transaction it is registered with has committed, and not when it rolled back (CORE1 Р21,
 * Р28): the listeners never hear of a change the database does not have.
 */
internal class CommitPublication(
    private val publisher: SardEventPublisher,
    private val event: SardEvent,
) : Synchronization {
    override fun beforeCompletion() {
        // Nothing to do before: only the outcome of the transaction matters.
    }

    override fun afterCompletion(status: Int) {
        if (status == Status.STATUS_COMMITTED) publisher.publish(event)
    }
}
