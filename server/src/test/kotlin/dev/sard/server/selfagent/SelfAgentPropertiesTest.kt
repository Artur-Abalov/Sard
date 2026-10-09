// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.selfagent

import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import java.nio.file.Path
import java.time.Duration
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertNull
import kotlin.test.assertTrue

/** Rules "Без канала механизм встроенного агента выключен" and "Проверка повторяется с заданным интервалом". */
@MutFlowTest
class SelfAgentPropertiesTest {
    @Test
    fun `Незаданный или пустой SARD_SELF_DIR выключает механизм`() {
        assertNull(MutFlow.underTest { SelfAgentProperties().settings() })
        assertNull(MutFlow.underTest { SelfAgentProperties(dir = "").settings() })
    }

    @Test
    fun `Интервал проверки по умолчанию - 15 секунд`() {
        val settings = MutFlow.underTest { SelfAgentProperties(dir = "/d").settings() }
        assertEquals(Duration.ofSeconds(15), settings?.checkInterval)
    }

    @Test
    fun `Интервал проверки и каталог берутся из настроек`() {
        val settings = MutFlow.underTest { SelfAgentProperties(dir = "/d", checkInterval = "5s").settings() }
        assertEquals(Duration.ofSeconds(5), settings?.checkInterval)
        assertEquals(Path.of("/d"), settings?.dir)
    }

    @Test
    fun `Недопустимый интервал называет настройку и значение`() {
        for (value in listOf("0s", "-5s", "abc")) {
            val message =
                assertFailsWith<IllegalArgumentException> {
                    MutFlow.underTest { SelfAgentProperties(dir = "/d", checkInterval = value).settings() }
                }.message.orEmpty()
            assertTrue("SARD_SELF_CHECK_INTERVAL" in message && "\"$value\"" in message, message)
        }
    }

    @Test
    fun `Недопустимый интервал не мешает серверу без канала`() {
        assertNull(MutFlow.underTest { SelfAgentProperties(dir = "", checkInterval = "abc").settings() })
    }
}
