// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.events

import dev.sard.server.extension.SardEvent
import dev.sard.server.extension.Subject
import org.slf4j.LoggerFactory
import org.springframework.context.ApplicationEvent
import org.springframework.context.ApplicationEventPublisher
import org.springframework.context.ApplicationListener
import org.springframework.context.PayloadApplicationEvent
import org.springframework.context.event.SimpleApplicationEventMulticaster

private val log = LoggerFactory.getLogger(SardEventPublisher::class.java)

/**
 * Who the events of the core name as their actor. Not a seam: [SubjectActors] reads the public
 * [dev.sard.server.extension.SubjectSource], which is what an extension replaces (CORE1 Р26).
 */
fun interface ActorSource {
    fun current(): Subject?

    companion object {
        val NONE = ActorSource { null }
    }
}

/**
 * Hands [SardEvent]s to the listeners of the application, synchronously (CORE1 Р21). Callers publish after the
 * change is committed. Nothing a listener does comes back to the caller (Р6).
 */
class SardEventPublisher(
    private val delegate: ApplicationEventPublisher,
) {
    fun publish(event: SardEvent) {
        runCatching { delegate.publishEvent(event) }
            .onFailure { log.warn("Event {} was not delivered: {}", event.type, it.toString()) }
    }

    companion object {
        /** Publishes to nobody; for the code that is built without a context. */
        val NONE = SardEventPublisher { }
    }
}

/**
 * The multicaster of the application: a listener that throws on a [SardEvent] is logged with the event's type and
 * skipped, so the others still get the event (Р6). Spring's own events keep the standard behaviour.
 */
class SardEventMulticaster : SimpleApplicationEventMulticaster() {
    override fun invokeListener(
        listener: ApplicationListener<*>,
        event: ApplicationEvent,
    ) {
        val sard = (event as? PayloadApplicationEvent<*>)?.payload as? SardEvent
        if (sard == null) {
            super.invokeListener(listener, event)
        } else {
            runCatching { super.invokeListener(listener, event) }
                .onFailure { log.warn("A listener failed on the event {}: {}", sard.type, it.toString()) }
        }
    }
}
