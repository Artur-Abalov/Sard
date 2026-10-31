// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.loop

import org.springframework.context.SmartLifecycle
import java.time.Duration
import java.util.concurrent.Executors
import java.util.concurrent.RejectedExecutionException
import java.util.concurrent.ScheduledExecutorService
import java.util.concurrent.TimeUnit

/** How a periodic task counts its interval: from the end of a run, or from the start of the one before. */
enum class Mode {
    /** The next run starts [Timing.interval] after the previous one ended; runs never overlap. */
    FIXED_DELAY,

    /** Runs start every [Timing.interval]; a slow run delays the next but they never overlap either. */
    FIXED_RATE,
}

/** When a periodic task runs: [initialDelay] after the start (one interval unless said otherwise), then [interval]. */
data class Timing(
    val interval: Duration,
    val initialDelay: Duration = interval,
    val mode: Mode = Mode.FIXED_DELAY,
)

/** Stopping waits for the run in progress for [patience] at most; [onTimeout] is called when it was still running. */
class StopWait(
    val patience: Duration,
    val onTimeout: () -> Unit,
)

/** Makes the single thread scheduler of one task; [threadName] is the name of its thread. */
fun interface SchedulerFactory {
    fun create(threadName: String): ScheduledExecutorService
}

/** A single thread scheduler of daemon threads named [threadName]: it never holds up the stop of the server. */
fun daemonScheduler(threadName: String): ScheduledExecutorService =
    Executors.newSingleThreadScheduledExecutor { Thread(it, threadName).apply { isDaemon = true } }

/**
 * Runs [action] by [timing] on one thread named [name] between [start] and [stop]. A run that throws is passed to
 * [onFailure] (nobody, when null) and never stops the later ones (an exception that escaped would cancel the periodic
 * task); an [onFailure] that throws is dropped likewise. [stop] interrupts a run in progress, waits for it as
 * [stopWait] says (not at all by default) and ends the thread.
 */
class PeriodicTask(
    private val name: String,
    private val timing: Timing,
    private val onFailure: ((Throwable) -> Unit)? = null,
    private val stopWait: StopWait? = null,
    private val schedulers: SchedulerFactory = SchedulerFactory(::daemonScheduler),
    private val action: () -> Unit,
) : SmartLifecycle {
    @Volatile
    private var executor: ScheduledExecutorService? = null

    override fun start() {
        val scheduler = schedulers.create(name)
        val initial = timing.initialDelay.toMillis()
        val period = timing.interval.toMillis()
        when (timing.mode) {
            Mode.FIXED_DELAY -> scheduler.scheduleWithFixedDelay(::run, initial, period, TimeUnit.MILLISECONDS)
            Mode.FIXED_RATE -> scheduler.scheduleAtFixedRate(::run, initial, period, TimeUnit.MILLISECONDS)
        }
        executor = scheduler
    }

    override fun stop() {
        executor?.let { scheduler ->
            scheduler.shutdownNow()
            stopWait?.let { wait ->
                if (!scheduler.awaitTermination(wait.patience.toMillis(), TimeUnit.MILLISECONDS)) wait.onTimeout()
            }
        }
        executor = null
    }

    override fun isRunning() = executor != null

    /** Asks for a run now, besides the periodic ones; ignored while the task is stopped or stopping. */
    fun runNow() {
        try {
            executor?.execute(::run)
        } catch (_: RejectedExecutionException) {
            // Stopping: a later start runs anyway.
        }
    }

    private fun run() {
        runCatching(action).onFailure { failure -> runCatching { onFailure?.invoke(failure) } }
    }
}
