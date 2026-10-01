// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.notify

import org.junit.jupiter.api.Test
import java.time.Duration
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicInteger
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

private val LONG: Duration = Duration.ofHours(1)

/** The loop ticks on its own thread; a finished run wakes it early (answer В4: asynchronous). */
class NotificationLoopTest {
    @Test
    fun `wake runs a tick without waiting for the interval`() {
        val ticked = CountDownLatch(1)
        val loop = NotificationLoop(LONG) { ticked.countDown() }
        loop.start()
        try {
            loop.wake()
            assertTrue(ticked.await(10, TimeUnit.SECONDS))
        } finally {
            loop.stop()
        }
    }

    @Test
    fun `a failing tick does not stop later ones`() {
        val calls = AtomicInteger()
        val second = CountDownLatch(2)
        val loop =
            NotificationLoop(LONG) {
                second.countDown()
                if (calls.incrementAndGet() == 1) error("database down")
            }
        loop.start()
        try {
            loop.wake()
            loop.wake()
            assertTrue(second.await(10, TimeUnit.SECONDS))
        } finally {
            loop.stop()
        }
    }

    @Test
    fun `wake is ignored while the loop is stopped`() {
        val calls = AtomicInteger()
        val loop = NotificationLoop(LONG) { calls.incrementAndGet() }
        loop.wake()
        loop.start()
        loop.stop()
        loop.wake()
        assertFalse(loop.isRunning)
        assertEquals(0, calls.get())
    }

    @Test
    fun `the interval ticks on its own`() {
        val ticked = CountDownLatch(2)
        val loop = NotificationLoop(Duration.ofMillis(10)) { ticked.countDown() }
        loop.start()
        try {
            assertTrue(ticked.await(10, TimeUnit.SECONDS))
            assertTrue(loop.isRunning)
        } finally {
            loop.stop()
        }
    }
}
