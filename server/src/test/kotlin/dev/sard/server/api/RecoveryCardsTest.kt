// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import kotlin.test.Test
import kotlin.test.assertEquals

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
        for (version in listOf("0.0.0-dev", "dev", "unknown", "0.1.0-rc", "0.1.0-beta.0", "01.2.3", "")) {
            assertEquals("$REPO/main/$DOC", MutFlow.underTest { recoveryProcedureUrl(version) }, version)
        }
    }
}
