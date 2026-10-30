// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.events

import dev.sard.server.extension.Subject
import dev.sard.server.extension.SubjectSource
import org.slf4j.LoggerFactory

private val log = LoggerFactory.getLogger(SubjectActors::class.java)

/**
 * The actor of an event is the subject the public [SubjectSource] names (CORE1 Р26). A source that throws must not
 * keep the event from being published: the actor is then null and one WARN is logged (Р31).
 */
class SubjectActors(
    private val subjects: SubjectSource,
) : ActorSource {
    override fun current(): Subject? =
        runCatching { subjects.current() }
            .getOrElse {
                log.warn("The subject source failed, the actor of the event is null: {}", it.message)
                null
            }
}
