// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.auth

import dev.sard.server.api.captureLogs
import dev.sard.server.loop.PeriodicTask
import dev.sard.server.pki.MovableClock
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import java.time.Clock
import java.time.Duration
import java.time.Instant
import java.time.ZoneId
import java.time.ZoneOffset
import java.util.UUID
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertFalse
import kotlin.test.assertTrue

private val LOGIN_AT = Instant.parse("2026-10-30T12:00:00Z")
private val SOME_TENANT = UUID.fromString("00000000-0000-0000-0000-000000000001")

/** Rule "Сессия, которой никто не пользуется, заканчивается заметно: событие приходит не позже интервала" (Р23). */
@MutFlowTest(excludeTargets = [PeriodicTask::class]) // PeriodicTaskTest mutates it
class SessionSweepTest {
    private val clock = MovableClock(LOGIN_AT)
    private val expired = mutableListOf<String>()
    private val swept = CountDownLatch(1)
    private val store =
        SessionStore(clock) { _, reason ->
            expired += reason
            swept.countDown()
        }

    @Test
    fun `The interval is one minute unless the property says otherwise`() {
        assertEquals(Duration.ofSeconds(60), MutFlow.underTest { SessionProperties().interval() })
        assertEquals(Duration.ofSeconds(5), MutFlow.underTest { SessionProperties(Duration.ofSeconds(5)).interval() })
    }

    @Test
    fun `A zero or negative interval is refused with the name of the property`() {
        for (bad in listOf(Duration.ZERO, Duration.ofSeconds(-1))) {
            val refusal =
                assertFailsWith<IllegalArgumentException> { MutFlow.underTest { SessionProperties(bad).interval() } }

            assertEquals("sard.session.sweep-interval must be positive", refusal.message)
        }
    }

    @Test
    fun `A sweep is not running before start and after stop`() {
        val sweep = SessionSweep(Duration.ofMinutes(1), store)
        assertFalse(sweep.isRunning)

        MutFlow.underTest { sweep.start() }
        assertTrue(sweep.isRunning)
        val thread = Thread.getAllStackTraces().keys.single { it.name == "sard-session-sweep" }
        assertTrue(thread.isDaemon, "the sweep must not keep the JVM alive")

        MutFlow.underTest { sweep.stop() }
        assertFalse(sweep.isRunning)
    }

    @Test
    fun `Stopping a sweep that never started is harmless`() {
        val sweep = SessionSweep(Duration.ofMinutes(1), store)

        MutFlow.underTest { sweep.stop() }

        assertFalse(sweep.isRunning)
    }

    @Test
    fun `A started sweep ends the sessions that ran out by itself, after the interval`() {
        store.create(SOME_TENANT)
        clock.now = LOGIN_AT + Duration.ofHours(1)
        val sweep = SessionSweep(Duration.ofMillis(50), store)

        MutFlow.underTest { sweep.start() }
        try {
            assertTrue(swept.await(10, TimeUnit.SECONDS), "the session was not ended by the sweep")
        } finally {
            sweep.stop()
        }

        assertEquals(listOf(IDLE), expired)
        assertEquals(0, store.trackedSessions())
    }

    @Test
    fun `A sweep that fails is logged and the next one still runs`() {
        val failures = CountDownLatch(1)
        val unreliable =
            object : Clock() {
                @Volatile
                var failing = false

                @Volatile
                var now: Instant = LOGIN_AT

                override fun getZone(): ZoneOffset = ZoneOffset.UTC

                override fun withZone(zone: ZoneId?): Clock = this

                override fun instant(): Instant {
                    if (failing) {
                        failures.countDown()
                        error("clock boom")
                    }
                    return now
                }
            }
        val unreliableStore =
            SessionStore(unreliable) { _, reason ->
                expired += reason
                swept.countDown()
            }
        unreliableStore.create(SOME_TENANT)
        unreliable.now = LOGIN_AT + Duration.ofHours(1)
        unreliable.failing = true
        val sweep = SessionSweep(Duration.ofMillis(50), unreliableStore)

        val logs =
            captureLogs {
                sweep.start()
                try {
                    assertTrue(failures.await(10, TimeUnit.SECONDS), "the sweep did not run")
                    unreliable.failing = false
                    assertTrue(swept.await(10, TimeUnit.SECONDS), "the sweep stopped after a failure")
                } finally {
                    sweep.stop()
                }
            }

        assertEquals(listOf(IDLE), expired)
        assertTrue(logs.any { "Session sweep failed; retrying at the next one" in it }, logs.toString())
        assertTrue(logs.any { "clock boom" in it }, logs.toString())
    }
}
