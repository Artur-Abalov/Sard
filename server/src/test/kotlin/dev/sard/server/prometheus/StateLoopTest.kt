// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.prometheus

import ch.qos.logback.classic.Level
import ch.qos.logback.classic.Logger
import ch.qos.logback.classic.spi.ILoggingEvent
import ch.qos.logback.core.read.ListAppender
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import org.slf4j.LoggerFactory
import java.time.Duration
import java.util.concurrent.CountDownLatch
import java.util.concurrent.ScheduledExecutorService
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicInteger
import java.util.concurrent.atomic.AtomicReference
import kotlin.test.AfterTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNull
import kotlin.test.assertTrue

/** The loop of the state tick: the first tick at once, then one per interval, on its own thread, never stopped. */
@MutFlowTest(includeTargets = [StateLoop::class])
class StateLoopTest {
    private val started = mutableListOf<StateLoop>()
    private val executors = mutableListOf<ScheduledExecutorService>()

    /**
     * As in RetentionLoopTest: only a call inside underTest has a mutation, so a loop is stopped outside it. A
     * mutated stop() that does nothing still clears the loop's own handle, and a later stop() would find nothing to
     * shut down, so the executor is taken at the start and shut down here directly: otherwise its thread would go on
     * ticking inside the mutation session of every later class.
     */
    @AfterTest
    fun `no loop thread outlives its test`() {
        started.forEach { it.stop() }
        started.clear()
        executors.forEach { it.shutdownNow() }
        executors.forEach { it.awaitTermination(10, TimeUnit.SECONDS) }
        executors.clear()
        val deadline = System.nanoTime() + TimeUnit.SECONDS.toNanos(10)
        while (System.nanoTime() < deadline && loopThreads().any { it.isAlive }) Thread.sleep(5)
        assertTrue(loopThreads().none { it.isAlive }, "a sard-metrics-state thread is still alive")
    }

    private fun loopThreads() = Thread.getAllStackTraces().keys.filter { it.name == "sard-metrics-state" }

    private fun StateLoop.begin() {
        started += this
        MutFlow.underTest { start() }
        remember()
    }

    /** Outside any session: the executor of a started loop, for the clean-up in [no loop thread outlives its test]. */
    private fun StateLoop.remember() {
        val field = StateLoop::class.java.getDeclaredField("executor").apply { isAccessible = true }
        (field.get(this) as ScheduledExecutorService?)?.let { executors += it }
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
    fun `Тик выполняется, пока сессия мутаций открыта, а не только после неё`() {
        val first = CountDownLatch(1)
        val loop = StateLoop(Duration.ofHours(1)) { first.countDown() }
        started += loop

        // The tick runs on the loop's own thread: it carries the mutant of the tick only if the session is still open.
        val ran =
            MutFlow.underTest {
                loop.start()
                first.await(10, TimeUnit.SECONDS)
            }
        loop.remember()

        loop.end()
        assertTrue(ran, "the tick did not run within 10 s of the start")
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

    private fun warningsOf(block: () -> Unit): List<ILoggingEvent> {
        val logger = LoggerFactory.getLogger(StateLoop::class.java) as Logger
        val appender = ListAppender<ILoggingEvent>().apply { start() }
        logger.addAppender(appender)
        try {
            block()
        } finally {
            logger.detachAppender(appender)
        }
        return appender.list.filter { it.level == Level.WARN }
    }

    @Test
    fun `Тик, не ушедший за время ожидания остановки, даёт одну WARN, а stop всё равно возвращается`() {
        val entered = CountDownLatch(1)
        val release = CountDownLatch(1)
        val loop =
            StateLoop(Duration.ofHours(1), Duration.ofMillis(100)) {
                entered.countDown()
                // Deaf to the interrupt, like a JDBC call may be.
                while (release.count > 0) {
                    try {
                        release.await()
                    } catch (_: InterruptedException) {
                        // keeps going
                    }
                }
            }
        loop.begin()
        try {
            assertTrue(entered.await(10, TimeUnit.SECONDS))

            val warnings = warningsOf { loop.end() }

            assertEquals(1, warnings.size)
            assertTrue(warnings.single().formattedMessage.contains("still running"))
            assertNull(warnings.single().throwableProxy)
            assertFalse(loop.isRunning)
        } finally {
            release.countDown()
        }
    }

    @Test
    fun `Тик, закончившийся за время ожидания остановки, не даёт предупреждения`() {
        val entered = CountDownLatch(1)
        val loop =
            StateLoop(Duration.ofHours(1), Duration.ofSeconds(10)) {
                entered.countDown()
                Thread.sleep(10_000)
            }
        loop.begin()
        assertTrue(entered.await(10, TimeUnit.SECONDS))

        val warnings = warningsOf { loop.end() }

        assertEquals(emptyList(), warnings)
    }
}
