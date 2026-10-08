// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.selfagent

import java.time.Duration
import java.util.concurrent.CountDownLatch
import java.util.concurrent.ScheduledExecutorService
import java.util.concurrent.ScheduledFuture
import java.util.concurrent.ScheduledThreadPoolExecutor
import java.util.concurrent.TimeUnit
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

/** A scheduler that remembers how it was asked to repeat a task and runs nothing. */
private class RecordingScheduler : ScheduledThreadPoolExecutor(1) {
    val requests = mutableListOf<Triple<Long, Long, TimeUnit>>()

    override fun scheduleWithFixedDelay(
        command: Runnable,
        initialDelay: Long,
        delay: Long,
        unit: TimeUnit,
    ): ScheduledFuture<*> {
        requests += Triple(initialDelay, delay, unit)
        return super.scheduleWithFixedDelay({}, LONG_HOURS, LONG_HOURS, TimeUnit.HOURS)
    }

    private companion object {
        const val LONG_HOURS = 24L
    }
}

/** Rule "Проверка повторяется с заданным интервалом" and "Первая проверка выполняется при старте сервера". */
class SelfAgentLoopTest {
    @Test
    fun `Цикл сначала готовит канал, затем сразу проверяет и повторяет проверку с заданным интервалом`() {
        val scheduler = RecordingScheduler()
        val events = mutableListOf<String>()
        val loop = SelfAgentLoop(Duration.ofSeconds(5), { scheduler }, { events += "setup" }, { events += "check" })

        loop.start()
        try {
            assertEquals(listOf("setup"), events)
            assertEquals(listOf(Triple(0L, 5000L, TimeUnit.MILLISECONDS)), scheduler.requests)
            assertTrue(loop.isRunning)
        } finally {
            loop.stop()
        }
        assertFalse(loop.isRunning)
        assertTrue(scheduler.isShutdown)
    }

    @Test
    fun `Проверки действительно выполняются повторно на своём потоке`() {
        val ticks = CountDownLatch(3)
        val names = mutableSetOf<String>()
        val loop =
            SelfAgentLoop(Duration.ofMillis(10), { ScheduledThreadPoolExecutor(1) }, {}) {
                names += Thread.currentThread().name
                ticks.countDown()
            }

        loop.start()
        try {
            assertTrue(ticks.await(10, TimeUnit.SECONDS))
        } finally {
            loop.stop()
        }
        assertFalse(Thread.currentThread().name in names)
    }

    @Test
    fun `Остановка незапущенного цикла ничего не делает`() {
        val created = mutableListOf<ScheduledExecutorService>()
        val loop = SelfAgentLoop(Duration.ofSeconds(1), { ScheduledThreadPoolExecutor(1).also(created::add) }, {}, {})

        loop.stop()

        assertFalse(loop.isRunning)
        assertEquals(emptyList(), created)
    }
}
