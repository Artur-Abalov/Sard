// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import dev.sard.server.install.AgentVersions
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

private const val DOC = "docs/operator/06-data-and-backup.md#восстановление-из-самобэкапа-f7"
private const val REPO = "https://github.com/Artur-Abalov/Sard/blob"

/** F4b, rules "Карточка восстановления…": the pure decisions. */
@MutFlowTest
class RecoveryCardsTest {
    @Test
    fun `Карточка версии релиза ссылается на тег этой версии`() {
        for (version in listOf("0.1.0", "0.1.0-beta.1", "1.20.3-rc.12")) {
            assertEquals("$REPO/v$version/$DOC", MutFlow.underTest { recoveryProcedureUrl(version) }, version)
        }
    }

    @Test
    fun `Карточка версии без тега ссылается на ветку main`() {
        val untagged =
            listOf(
                "0.0.0-dev",
                "dev",
                "unknown",
                "0.1.0-rc",
                "0.1.0-beta.0",
                "01.2.3",
                "",
                "99999999999999999999.0.0",
                "1.2.99999999999999999999",
                "1.2.3-beta.99999999999999999999",
            )
        for (version in untagged) {
            assertEquals("$REPO/main/$DOC", MutFlow.underTest { recoveryProcedureUrl(version) }, version)
        }
    }

    private fun outdated(
        agent: String,
        offered: String,
    ) = MutFlow.underTest { AgentVersions(offered).outdated(agent) }

    @Test
    fun `Тот же разбор версии упорядочивает beta, rc и релиз`() {
        assertTrue(outdated("v1.0.0-beta.1", offered = "v1.0.0"))
        assertTrue(outdated("v0.9.9", offered = "v1.0.0"))
        assertFalse(outdated("v1.0.0", offered = "v1.0.0"))
        assertTrue(outdated("v1.0.0-beta.2", offered = "v1.0.0-rc.1"))
        assertTrue(outdated("v1.0.0-rc.1", offered = "v1.0.0"))
        assertFalse(outdated("v1.0.0", offered = "v1.0.0-rc.1"))
        assertFalse(outdated("v1.0.0-rc.1", offered = "v1.0.0-beta.2"))
        assertFalse(outdated("v99999999999999999999.0.0", offered = "v1.0.0"))
        assertFalse(outdated("v1.0.0-rc.99999999999999999999", offered = "v1.0.0"))
    }
}
