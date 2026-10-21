// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.retention

import org.junit.jupiter.api.extension.ExtendWith
import org.springframework.boot.test.system.CapturedOutput
import org.springframework.boot.test.system.OutputCaptureExtension
import java.time.Duration
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicInteger
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

/** The loop of the cleaning: the first pass one interval after the start, a failing pass does not stop the next. */
@ExtendWith(OutputCaptureExtension::class)
class RetentionLoopTest {
    @Test
    fun `До истечения интервала после старта прохода нет, потом он наступает`() {
        val passes = AtomicInteger()
        val first = CountDownLatch(1)
        val loop =
            RetentionLoop(Duration.ofMillis(600)) {
                passes.incrementAndGet()
                first.countDown()
            }
        loop.start()
        try {
            Thread.sleep(200)
            assertEquals(0, passes.get(), "no pass before one interval has gone")
            assertTrue(first.await(10, TimeUnit.SECONDS))
            assertTrue(loop.isRunning)
        } finally {
            loop.stop()
        }
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
        loop.start()
        try {
            assertTrue(second.await(10, TimeUnit.SECONDS))
        } finally {
            loop.stop()
        }

        val errors = output.out.lines().filter { "ERROR" in it && "Retention pass failed" in it }
        assertTrue(errors.first().contains("java.lang.IllegalStateException"), errors.toString())
        assertFalse("secret line of a log" in output.out)
    }

    @Test
    fun `Остановленный цикл не начинает новых проходов`() {
        val passes = AtomicInteger()
        val loop = RetentionLoop(Duration.ofMillis(50)) { passes.incrementAndGet() }
        loop.start()
        loop.stop()
        val atStop = passes.get()

        Thread.sleep(300)

        assertFalse(loop.isRunning)
        assertEquals(atStop, passes.get())
    }
}
