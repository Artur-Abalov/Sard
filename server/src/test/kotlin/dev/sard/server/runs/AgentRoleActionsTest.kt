// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.runs

import dev.sard.server.role.AgentRole
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import kotlin.test.Test
import kotlin.test.assertEquals

/** V1: a source runs BACKUP, RESTORE and RUN, a verifier only VERIFY. */
@MutFlowTest
class AgentRoleActionsTest {
    private fun allowed(role: AgentRole): Set<Action> {
        val actions = Action.entries.filter { MutFlow.underTest { role.allows(it) } }
        return actions.toSet()
    }

    @Test
    fun `a source may be asked to back up, restore and run, never to verify`() {
        assertEquals(setOf(Action.BACKUP, Action.RESTORE, Action.RUN), allowed(AgentRole.SOURCE))
    }

    @Test
    fun `a verifier may be asked to verify and nothing else`() {
        assertEquals(setOf(Action.VERIFY), allowed(AgentRole.VERIFIER))
    }
}
