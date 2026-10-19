// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.fleet

import dev.sard.server.api.captureLogs
import java.time.Duration
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

/** C1 phase 3, П35: a failed check is one warning, and the next one runs. */
class AgentCertificateLoopTest {
    @Test
    fun `Сбой проверки пишет одну строку WARN, и цикл проверки продолжает работу`() {
        val ticks = CountDownLatch(4)
        val loop =
            AgentCertificateLoop(Duration.ofMillis(10)) {
                ticks.countDown()
                error("the database is down")
            }

        val logs =
            captureLogs {
                loop.start()
                assertTrue(ticks.await(10, TimeUnit.SECONDS), "the loop stopped after a failure")
                loop.stop()
            }

        val warnings = logs.filter { "Agent certificate check failed; retrying at the next one" in it }
        assertTrue(warnings.size >= 3, "$logs")
    }

    @Test
    fun `Цикл запущен, пока не остановлен`() {
        val loop = AgentCertificateLoop(Duration.ofHours(1)) {}
        assertEquals(false, loop.isRunning)
        loop.start()
        assertEquals(true, loop.isRunning)
        loop.stop()
        assertEquals(false, loop.isRunning)
    }
}
