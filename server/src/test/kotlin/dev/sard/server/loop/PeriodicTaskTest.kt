// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.loop

import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import java.time.Duration
import java.util.concurrent.CountDownLatch
import java.util.concurrent.RejectedExecutionException
import java.util.concurrent.ScheduledExecutorService
import java.util.concurrent.ScheduledFuture
import java.util.concurrent.ScheduledThreadPoolExecutor
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicReference
import kotlin.test.AfterTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertSame
import kotlin.test.assertTrue

private const val FAR_HOURS = 24L

/** What the task asked its scheduler for. */
private data class Request(
    val kind: String,
    val initialDelay: Long,
    val period: Long,
    val unit: TimeUnit,
)

/** A scheduler that records how it was asked to repeat a task, keeps the task to run by hand and never runs it. */
private class FakeScheduler(
    private val terminates: Boolean = true,
) : ScheduledThreadPoolExecutor(1, { Thread(it).apply { isDaemon = true } }) {
    val requests = mutableListOf<Request>()
    val tasks = mutableListOf<Runnable>()
    val awaited = mutableListOf<Pair<Long, TimeUnit>>()
    var rejecting = false

    override fun scheduleWithFixedDelay(
        command: Runnable,
        initialDelay: Long,
        delay: Long,
        unit: TimeUnit,
    ): ScheduledFuture<out Any?> {
        requests += Request("delay", initialDelay, delay, unit)
        tasks += command
        return super.scheduleWithFixedDelay(NOTHING, FAR_HOURS, FAR_HOURS, TimeUnit.HOURS)
    }

    override fun awaitTermination(
        timeout: Long,
        unit: TimeUnit,
    ): Boolean {
        awaited += timeout to unit
        return terminates
    }

    override fun scheduleAtFixedRate(
        command: Runnable,
        initialDelay: Long,
        period: Long,
        unit: TimeUnit,
    ): ScheduledFuture<out Any?> {
        requests += Request("rate", initialDelay, period, unit)
        tasks += command
        return super.scheduleWithFixedDelay(NOTHING, FAR_HOURS, FAR_HOURS, TimeUnit.HOURS)
    }

    override fun execute(command: Runnable) {
        if (rejecting) throw RejectedExecutionException("stopping")
        tasks += command
    }
}

private val NOTHING = Runnable { }
private val INTERVAL: Duration = Duration.ofSeconds(5)

/** The shared periodic loop: how it schedules, how it survives a failing run, how it stops. */
@MutFlowTest(includeTargets = [PeriodicTask::class])
class PeriodicTaskTest {
    private val schedulers = mutableListOf<ScheduledExecutorService>()
    private val failures = mutableListOf<Throwable>()
    private val names = mutableListOf<String>()

    @AfterTest
    fun `no scheduler outlives its test`() {
        schedulers.forEach { it.shutdownNow() }
    }

    private fun fake(terminates: Boolean = true) = FakeScheduler(terminates).also { schedulers += it }

    private fun task(
        timing: Timing = Timing(INTERVAL),
        scheduler: FakeScheduler = fake(),
        stopWait: StopWait? = null,
        action: () -> Unit = {},
    ) = PeriodicTask(
        name = "sard-test",
        timing = timing,
        onFailure = { failures += it },
        stopWait = stopWait,
        schedulers =
            SchedulerFactory {
                names += it
                scheduler
            },
        action = action,
    )

    @Test
    fun `A fixed delay loop waits one interval for the first run and one interval after each run`() {
        val scheduler = fake()
        val loop = task(scheduler = scheduler)

        MutFlow.underTest { loop.start() }

        assertEquals(listOf(Request("delay", 5000, 5000, TimeUnit.MILLISECONDS)), scheduler.requests)
        assertEquals(listOf("sard-test"), names)
        MutFlow.underTest { loop.stop() }
    }

    @Test
    fun `An initial delay of zero runs the first time at once`() {
        val scheduler = fake()
        val loop = task(Timing(INTERVAL, initialDelay = Duration.ZERO), scheduler)

        MutFlow.underTest { loop.start() }

        assertEquals(listOf(Request("delay", 0, 5000, TimeUnit.MILLISECONDS)), scheduler.requests)
        MutFlow.underTest { loop.stop() }
    }

    @Test
    fun `A fixed rate loop schedules at a fixed rate with its own initial delay`() {
        val scheduler = fake()
        val timing = Timing(Duration.ofSeconds(7), Duration.ofSeconds(2), Mode.FIXED_RATE)
        val loop = task(timing, scheduler)

        MutFlow.underTest { loop.start() }

        assertEquals(listOf(Request("rate", 2000, 7000, TimeUnit.MILLISECONDS)), scheduler.requests)
        MutFlow.underTest { loop.stop() }
    }

    @Test
    fun `The loop runs only between start and stop, and stop shuts the scheduler down`() {
        val scheduler = fake()
        val loop = task(scheduler = scheduler)
        assertFalse(MutFlow.underTest { loop.isRunning })

        MutFlow.underTest { loop.start() }
        assertTrue(MutFlow.underTest { loop.isRunning })
        assertFalse(scheduler.isShutdown)

        MutFlow.underTest { loop.stop() }
        assertFalse(MutFlow.underTest { loop.isRunning })
        assertTrue(scheduler.isShutdown)
        assertEquals(emptyList(), scheduler.awaited)
    }

    @Test
    fun `Stopping a loop that never started creates and shuts down nothing`() {
        val scheduler = fake()
        val loop = task(scheduler = scheduler)

        MutFlow.underTest { loop.stop() }

        assertFalse(MutFlow.underTest { loop.isRunning })
        assertEquals(emptyList(), names)
        assertFalse(scheduler.isShutdown)
    }

    @Test
    fun `A loop starts again after it was stopped`() {
        val loop =
            PeriodicTask("sard-test", Timing(INTERVAL), { failures += it }, schedulers = { fake() }) {}

        MutFlow.underTest { loop.start() }
        MutFlow.underTest { loop.stop() }
        MutFlow.underTest { loop.start() }

        assertTrue(MutFlow.underTest { loop.isRunning })
        assertEquals(2, schedulers.size)
        assertTrue(schedulers.first().isShutdown)
        assertFalse(schedulers.last().isShutdown)
    }

    @Test
    fun `A run does the action and reports nothing`() {
        var runs = 0
        val scheduler = fake()
        val loop = task(scheduler = scheduler) { runs++ }
        MutFlow.underTest { loop.start() }

        MutFlow.underTest { scheduler.tasks.single().run() }

        assertEquals(1, runs)
        assertEquals(emptyList(), failures)
        MutFlow.underTest { loop.stop() }
    }

    @Test
    fun `A failing run is reported and does not escape, so the next run still happens`() {
        val boom = IllegalStateException("boom")
        var runs = 0
        val scheduler = fake()
        val loop =
            task(scheduler = scheduler) {
                runs++
                if (runs == 1) throw boom
            }
        MutFlow.underTest { loop.start() }
        val run = scheduler.tasks.single()

        MutFlow.underTest { run.run() }
        MutFlow.underTest { run.run() }

        assertEquals(2, runs)
        assertEquals(1, failures.size)
        assertSame(boom, failures.single())
        MutFlow.underTest { loop.stop() }
    }

    @Test
    fun `A run that throws an error is reported too`() {
        val scheduler = fake()
        val loop = task(scheduler = scheduler) { throw AssertionError("an error") }
        MutFlow.underTest { loop.start() }

        MutFlow.underTest { scheduler.tasks.single().run() }

        assertEquals(1, failures.size)
        assertTrue(failures.single() is AssertionError)
        MutFlow.underTest { loop.stop() }
    }

    @Test
    fun `runNow runs the action on the scheduler once, through the same guard`() {
        var runs = 0
        val scheduler = fake()
        val loop =
            task(scheduler = scheduler) {
                runs++
                error("boom")
            }
        MutFlow.underTest { loop.start() }

        MutFlow.underTest { loop.runNow() }
        assertEquals(2, scheduler.tasks.size)
        MutFlow.underTest { scheduler.tasks.last().run() }

        assertEquals(1, runs)
        assertEquals(1, failures.size)
        MutFlow.underTest { loop.stop() }
    }

    @Test
    fun `runNow before start and after stop does nothing`() {
        val scheduler = fake()
        val loop = task(scheduler = scheduler)

        MutFlow.underTest { loop.runNow() }
        MutFlow.underTest { loop.start() }
        MutFlow.underTest { loop.stop() }
        MutFlow.underTest { loop.runNow() }

        assertEquals(1, scheduler.tasks.size)
    }

    @Test
    fun `runNow on a scheduler that is going down is ignored`() {
        val scheduler = fake()
        val loop = task(scheduler = scheduler)
        MutFlow.underTest { loop.start() }
        scheduler.rejecting = true

        MutFlow.underTest { loop.runNow() }

        assertEquals(1, scheduler.tasks.size)
        MutFlow.underTest { loop.stop() }
    }

    @Test
    fun `A stop that waits gives the running run its patience and says nothing when the run is over`() {
        val scheduler = fake(terminates = true)
        var told = 0
        val loop = task(scheduler = scheduler, stopWait = StopWait(Duration.ofSeconds(3)) { told++ })
        MutFlow.underTest { loop.start() }

        MutFlow.underTest { loop.stop() }

        assertEquals(listOf(3000L to TimeUnit.MILLISECONDS), scheduler.awaited)
        assertEquals(0, told)
        assertTrue(scheduler.isShutdown)
        assertFalse(MutFlow.underTest { loop.isRunning })
    }

    @Test
    fun `A stop that waits tells once when the run did not end in time, and returns`() {
        val scheduler = fake(terminates = false)
        var told = 0
        val loop = task(scheduler = scheduler, stopWait = StopWait(Duration.ofSeconds(3)) { told++ })
        MutFlow.underTest { loop.start() }

        MutFlow.underTest { loop.stop() }

        assertEquals(1, told)
        assertFalse(MutFlow.underTest { loop.isRunning })
    }

    @Test
    fun `The default scheduler is a daemon thread of the given name`() {
        val ran = CountDownLatch(1)
        val thread = AtomicReference<Thread>()
        val loop =
            PeriodicTask(
                name = "sard-periodic-test",
                timing = Timing(Duration.ofHours(1), initialDelay = Duration.ZERO),
                onFailure = { failures += it },
            ) {
                thread.set(Thread.currentThread())
                ran.countDown()
            }

        MutFlow.underTest { loop.start() }
        try {
            assertTrue(ran.await(10, TimeUnit.SECONDS))
        } finally {
            loop.stop()
        }

        assertEquals("sard-periodic-test", thread.get().name)
        assertTrue(thread.get().isDaemon)
        assertEquals(emptyList(), failures)
    }
}
