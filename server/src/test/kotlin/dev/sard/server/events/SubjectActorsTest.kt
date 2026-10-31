// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.events

import dev.sard.server.api.captureLogs
import dev.sard.server.extension.Subject
import dev.sard.server.extension.SubjectSource
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNull

private val ROBOT = Subject("svc", "robot")

/** Rule "Кто выполняет запрос, политика и события узнают из одного шва SubjectSource" (Р26, Р31): the actor. */
@MutFlowTest
class SubjectActorsTest {
    @Test
    fun `the actor of an event is the subject the source names`() {
        assertEquals(ROBOT, MutFlow.underTest { SubjectActors { ROBOT }.current() })
    }

    @Test
    fun `a source that names nobody gives no actor`() {
        assertNull(MutFlow.underTest { SubjectActors { null }.current() })
    }

    @Test
    fun `a source that throws gives no actor and one WARN, so the event is still delivered`() {
        var actor: Subject? = ROBOT
        val broken = SubjectSource { error("sso unreachable") }

        val logs = captureLogs { actor = MutFlow.underTest { SubjectActors(broken).current() } }

        assertNull(actor)
        assertEquals(1, logs.count { "subject source failed" in it }, logs.toString())
    }
}
