// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.events

import dev.sard.server.extension.SardEvent
import dev.sard.server.extension.SettingsChanged
import dev.sard.server.extension.Subject
import dev.sard.server.pki.MovableClock
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import java.time.Instant
import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

private val AT = Instant.parse("2026-10-30T12:00:00Z")
private val TENANT = UUID.fromString("00000000-0000-0000-0000-0000000000aa")

/** Rule "Изменение настроек даёт событие settings.changed, только если состояние изменилось" (Р20). */
@MutFlowTest
class SettingsEventsTest {
    private val published = mutableListOf<SardEvent>()
    private val actor = Subject("administrator", "admin")
    private val events =
        SettingsEvents(SardEventPublisher { published += it as SardEvent }, { actor }, MovableClock(AT)) { TENANT }

    @Test
    fun `changed publishes the setting, the object, the tenant, the actor and the time`() {
        val objectId = UUID.randomUUID()

        MutFlow.underTest { events.changed("source_created", objectId) }

        assertEquals(listOf<SardEvent>(SettingsChanged(AT, TENANT, actor, "source_created", objectId)), published)
    }

    @Test
    fun `changed without an object names none`() {
        MutFlow.underTest { events.changed("password") }

        assertEquals(listOf<SardEvent>(SettingsChanged(AT, TENANT, actor, "password", null)), published)
    }
}
