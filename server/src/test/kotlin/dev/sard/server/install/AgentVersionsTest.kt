// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.install

import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import kotlin.test.Test
import kotlin.test.assertEquals

/** Rule "Агент старше раздаваемой версии помечен как устаревший" (В4). */
@MutFlowTest
class AgentVersionsTest {
    private fun outdated(
        agent: String?,
        offered: String = "v1.4.0",
    ) = MutFlow.underTest { AgentVersions(offered).outdated(agent) }

    @Test
    fun `Агент v1_3_2 при раздаче v1_4_0 устарел`() {
        assertEquals(true, outdated("v1.3.2"))
    }

    @Test
    fun `Агент той же версии не устарел`() {
        assertEquals(false, outdated("v1.4.0"))
    }

    @Test
    fun `Агент новее раздаваемой версии не устарел`() {
        assertEquals(false, outdated("v1.5.0"))
        assertEquals(false, outdated("v2.0.0"))
    }

    @Test
    fun `Сравнение версий числовое, а не строковое`() {
        assertEquals(false, outdated("v1.10.0", offered = "v1.9.0"))
        assertEquals(true, outdated("v1.9.0", offered = "v1.10.0"))
    }

    @Test
    fun `Отличие только в патче и только в старшей версии учитывается`() {
        assertEquals(true, outdated("v1.4.0", offered = "v1.4.1"))
        assertEquals(true, outdated("v1.9.9", offered = "v2.0.0"))
    }

    @Test
    fun `Нерелизная версия агента не помечается`() {
        assertEquals(false, outdated("v1.3.2-5-gabc1234-dirty"))
        assertEquals(false, outdated("dev"))
        assertEquals(false, outdated("1.3.2"))
        assertEquals(false, outdated("v1.3"))
        assertEquals(false, outdated("v1.3.2-rc1"))
        assertEquals(false, outdated("v01.3.2"))
        assertEquals(false, outdated("v99999999999999999999.0.0"))
    }

    @Test
    fun `Агент без версии не помечается`() {
        assertEquals(false, outdated(null))
    }

    @Test
    fun `Нерелизная сборка сервера не помечает никого`() {
        assertEquals(false, outdated("v1.3.2", offered = "0.0.0-dev"))
        assertEquals(false, outdated("v1.3.2", offered = "dev"))
    }
}
