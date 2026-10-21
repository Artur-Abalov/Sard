// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.retention

import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import org.junit.jupiter.api.extension.ExtendWith
import org.springframework.boot.test.system.CapturedOutput
import org.springframework.boot.test.system.OutputCaptureExtension
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

/** The loop of the cleaning: the first pass one interval after the start, a failing pass does not stop the next. */
@MutFlowTest(includeTargets = [RetentionLoop::class])
@ExtendWith(OutputCaptureExtension::class)
class RetentionLoopTest {
    private val started = mutableListOf<RetentionLoop>()

    /**
     * mutflow's session is one static per JVM and only a call inside underTest has a mutation: a loop whose mutated
     * stop() did nothing would go on firing passes into the session of every later class. The plain stop() here is
     * outside any session, so it always stops the thread, and the thread has gone before the next test begins.
     */
    @AfterTest
    fun `no loop thread outlives its test`() {
        started.forEach { it.stop() }
        started.clear()
        val deadline = System.nanoTime() + TimeUnit.SECONDS.toNanos(10)
        while (System.nanoTime() < deadline && loopThreads().any { it.isAlive }) Thread.sleep(5)
        assertTrue(loopThreads().none { it.isAlive }, "a sard-retention thread is still alive")
    }

    private fun loopThreads() = Thread.getAllStackTraces().keys.filter { it.name == "sard-retention" }

    private fun RetentionLoop.begin() {
        started += this
        MutFlow.underTest { start() }
    }

    private fun RetentionLoop.end() = MutFlow.underTest { stop() }

    private fun RetentionLoop.running() = MutFlow.underTest { isRunning }

    @Test
    fun `До истечения интервала после старта прохода нет, потом он наступает`() {
        val passes = AtomicInteger()
        val first = CountDownLatch(1)
        val loop =
            RetentionLoop(Duration.ofMillis(600)) {
                passes.incrementAndGet()
                first.countDown()
            }
        loop.begin()
        try {
            Thread.sleep(200)
            assertEquals(0, passes.get(), "no pass before one interval has gone")
            assertTrue(first.await(10, TimeUnit.SECONDS))
            assertTrue(loop.running())
        } finally {
            loop.end()
        }
    }

    @Test
    fun `Проход идёт в потоке-демоне sard-retention, который не держит остановку сервера`() {
        val worker = AtomicReference<Thread>()
        val ran = CountDownLatch(1)
        val loop =
            RetentionLoop(Duration.ofMillis(10)) {
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
        assertEquals("sard-retention", worker.get().name)
    }

    @Test
    fun `Ошибка прохода пишется в журнал с классом исключения и не останавливает цикл`(output: CapturedOutput) {
        val calls = AtomicInteger()
        val second = CountDownLatch(2)
        val loop =
            RetentionLoop(Duration.ofMillis(10)) {
                second.countDown()
                if (calls.incrementAndGet() == 1) throw IllegalStateException("secret line of a log")
            }
        loop.begin()
        try {
            assertTrue(second.await(10, TimeUnit.SECONDS))
        } finally {
            loop.end()
        }

        val errors = output.out.lines().filter { "ERROR" in it && "Retention pass failed" in it }
        assertTrue(errors.first().contains("java.lang.IllegalStateException"), errors.toString())
        assertFalse("secret line of a log" in output.out)
    }

    @Test
    fun `Остановленный цикл не начинает новых проходов`() {
        val passes = AtomicInteger()
        val loop = RetentionLoop(Duration.ofMillis(50)) { passes.incrementAndGet() }
        loop.begin()
        loop.end()
        val atStop = passes.get()

        Thread.sleep(300)

        assertFalse(loop.running())
        assertEquals(atStop, passes.get())
    }
}
