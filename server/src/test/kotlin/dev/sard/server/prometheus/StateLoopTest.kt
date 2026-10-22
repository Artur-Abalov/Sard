// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.prometheus

import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import java.time.Duration
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicInteger
import java.util.concurrent.atomic.AtomicReference
import kotlin.test.AfterTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

/** The loop of the state tick: the first tick at once, then one per interval, on its own thread, never stopped. */
@MutFlowTest(includeTargets = [StateLoop::class])
class StateLoopTest {
    private val started = mutableListOf<StateLoop>()

    /** As in RetentionLoopTest: only a call inside underTest has a mutation, so a loop is stopped outside it. */
    @AfterTest
    fun `no loop thread outlives its test`() {
        started.forEach { it.stop() }
        started.clear()
        val deadline = System.nanoTime() + TimeUnit.SECONDS.toNanos(10)
        while (System.nanoTime() < deadline && loopThreads().any { it.isAlive }) Thread.sleep(5)
        assertTrue(loopThreads().none { it.isAlive }, "a sard-metrics-state thread is still alive")
    }

    private fun loopThreads() = Thread.getAllStackTraces().keys.filter { it.name == "sard-metrics-state" }

    private fun StateLoop.begin() {
        started += this
        MutFlow.underTest { start() }
    }

    private fun StateLoop.end() = MutFlow.underTest { stop() }

    @Test
    fun `Первый тик проходит сразу после старта, не ждёт интервала`() {
        val first = CountDownLatch(1)
        val loop = StateLoop(Duration.ofHours(1)) { first.countDown() }

        loop.begin()
        try {
            assertTrue(first.await(10, TimeUnit.SECONDS), "no tick within 10 s of the start")
            assertTrue(MutFlow.underTest { loop.isRunning })
        } finally {
            loop.end()
        }
    }

    @Test
    fun `Следующие тики идут по интервалу`() {
        val ticks = AtomicInteger()
        val third = CountDownLatch(3)
        val loop =
            StateLoop(Duration.ofMillis(50)) {
                ticks.incrementAndGet()
                third.countDown()
            }

        loop.begin()
        try {
            assertTrue(third.await(10, TimeUnit.SECONDS))
        } finally {
            loop.end()
        }
    }

    @Test
    fun `Тик идёт в потоке-демоне sard-metrics-state, который не держит остановку сервера`() {
        val worker = AtomicReference<Thread>()
        val ran = CountDownLatch(1)
        val loop =
            StateLoop(Duration.ofMillis(10)) {
                worker.set(Thread.currentThread())
                ran.countDown()
            }
        loop.begin()
        try {
            assertTrue(ran.await(10, TimeUnit.SECONDS))
        } finally {
            loop.end()
        }

        assertTrue(worker.get().isDaemon)
        assertEquals("sard-metrics-state", worker.get().name)
    }

    @Test
    fun `Сбой тика не останавливает цикл`() {
        val calls = AtomicInteger()
        val second = CountDownLatch(2)
        val loop =
            StateLoop(Duration.ofMillis(10)) {
                second.countDown()
                if (calls.incrementAndGet() == 1) throw IllegalStateException("the tick failed")
            }

        loop.begin()
        try {
            assertTrue(second.await(10, TimeUnit.SECONDS))
        } finally {
            loop.end()
        }
    }

    @Test
    fun `Остановленный цикл не начинает новых тиков`() {
        val ticks = AtomicInteger()
        val loop = StateLoop(Duration.ofMillis(50)) { ticks.incrementAndGet() }
        loop.begin()
        loop.end()
        val atStop = ticks.get()

        Thread.sleep(300)

        assertFalse(loop.isRunning)
        assertEquals(atStop, ticks.get())
    }

    @Test
    fun `Тик, который ещё идёт, не перекрывается следующим`() {
        val inside = AtomicInteger()
        val overlapped =
            java.util.concurrent.atomic
                .AtomicBoolean()
        val done = CountDownLatch(3)
        val loop =
            StateLoop(Duration.ofMillis(5)) {
                if (inside.incrementAndGet() > 1) overlapped.set(true)
                Thread.sleep(30)
                inside.decrementAndGet()
                done.countDown()
            }
        loop.begin()
        try {
            assertTrue(done.await(10, TimeUnit.SECONDS))
        } finally {
            loop.end()
        }

        assertFalse(overlapped.get())
    }

    @Test
    fun `Когда stop вернулся, идущий тик закончен и новых нет`() {
        val inside =
            java.util.concurrent.atomic
                .AtomicBoolean()
        val entered = CountDownLatch(1)
        val ticks = AtomicInteger()
        val loop =
            StateLoop(Duration.ofMillis(5)) {
                ticks.incrementAndGet()
                inside.set(true)
                entered.countDown()
                try {
                    Thread.sleep(200)
                } finally {
                    inside.set(false)
                }
            }
        loop.begin()
        assertTrue(entered.await(10, TimeUnit.SECONDS))

        loop.end()

        assertFalse(inside.get(), "a tick still runs after stop() returned")
        val atStop = ticks.get()
        Thread.sleep(100)
        assertEquals(atStop, ticks.get())
    }
}
