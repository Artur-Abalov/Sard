// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.install

import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import java.io.File
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

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

    @Test
    fun `Порядок сравнения совпадает с цепочкой version-order`() {
        val path = checkNotNull(System.getProperty("sard.test.version-order-file")) { "run through Gradle" }
        val chain = File(path).readLines().map(String::trim).filter { it.isNotEmpty() && !it.startsWith("#") }
        assertTrue(chain.size >= 10, "the chain has ${chain.size} versions")
        for ((i, agent) in chain.withIndex()) {
            for ((j, offered) in chain.withIndex()) {
                assertEquals(i < j, outdated(agent, offered), "agent $agent, offered $offered")
            }
        }
    }

    @Test
    fun `Номер предрелиза сравнивается как число`() {
        assertEquals(true, outdated("v0.1.0-beta.2", offered = "v0.1.0-beta.10"))
        assertEquals(false, outdated("v0.1.0-beta.10", offered = "v0.1.0-beta.2"))
        assertEquals(true, outdated("v0.1.0-rc.2", offered = "v0.1.0-rc.10"))
    }

    @Test
    fun `Бета старше rc той же версии`() {
        assertEquals(true, outdated("v0.1.0-beta.10", offered = "v0.1.0-rc.1"))
        assertEquals(false, outdated("v0.1.0-rc.1", offered = "v0.1.0-beta.10"))
    }

    @Test
    fun `Предрелиз старше релиза той же версии`() {
        assertEquals(true, outdated("v0.1.0-rc.10", offered = "v0.1.0"))
        assertEquals(false, outdated("v0.1.0", offered = "v0.1.0-rc.10"))
    }

    @Test
    fun `Предрелиз следующей версии новее предыдущего релиза`() {
        assertEquals(true, outdated("v0.1.0", offered = "v0.1.1-beta.1"))
        assertEquals(false, outdated("v0.2.0-beta.1", offered = "v0.1.0"))
    }

    @Test
    fun `Одинаковые предрелизы не устарели`() {
        assertEquals(false, outdated("v0.1.0-beta.1", offered = "v0.1.0-beta.1"))
    }

    @Test
    fun `Версия агента вне правила тегов релиза не помечается`() {
        val notReleases =
            listOf(
                "v0.0.1-rc1",
                "v0.1.0-alpha.1",
                "v0.1.0-rc",
                "v0.1.0-beta.0",
                "v0.1.0-rc.01",
                "v0.01.0",
                "v0.1.0-RC.1",
                "v0.1.0-rc.1.1",
                "v0.1.0+build",
                "0.0.9",
                "v0.0.9-beta.1-5-gabc1234",
                "v0.0.9-5-gabc1234",
                "dev",
                "",
            )
        for (agent in notReleases) assertEquals(false, outdated(agent, offered = "v0.1.0"), agent)
    }

    @Test
    fun `Сервер с версией вне правила тегов релиза не помечает никого`() {
        val notReleases = listOf("v0.0.1-rc1", "v0.1.0-alpha.1", "v0.1.0-beta.1-5-gabc1234", "0.0.0-dev")
        for (offered in notReleases) assertEquals(false, outdated("v0.0.1-beta.1", offered), offered)
    }

    @Test
    fun `Число больше Long в версии не делает её релизной`() {
        assertEquals(false, outdated("v0.1.0-beta.99999999999999999999", offered = "v0.1.0"))
        assertEquals(false, outdated("v0.1.0", offered = "v99999999999999999999.0.0"))
        assertEquals(false, outdated("v0.1.0-beta.1", offered = "v0.1.0-beta.99999999999999999999"))
    }
}
