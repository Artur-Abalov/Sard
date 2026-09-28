// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents.stream

import java.time.Duration
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import kotlin.test.Test
import kotlin.test.assertFalse
import kotlin.test.assertTrue

class AgentStreamSweeperTest {
    @Test
    fun `a failing check does not stop the next ones`() {
        val runs = CountDownLatch(3)
        val sweeper =
            AgentStreamSweeper(Duration.ofMillis(1)) {
                runs.countDown()
                error("database unavailable")
            }
        sweeper.start()
        try {
            assertTrue(sweeper.isRunning)
            assertTrue(runs.await(WAIT_SECONDS, TimeUnit.SECONDS), "the check ran again after it threw")
        } finally {
            sweeper.stop()
        }
        assertFalse(sweeper.isRunning)
    }
}
