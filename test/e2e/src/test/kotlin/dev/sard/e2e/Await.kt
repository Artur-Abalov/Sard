// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.e2e

import java.time.Duration
import java.time.Instant
import kotlin.test.fail

/** Waiting by polling with a deadline: the tests never sleep a fixed time for a status. */
internal object Await {
    val TIMEOUT: Duration = Duration.ofSeconds(60)
    private val POLL = Duration.ofMillis(500)

    /** Polls [condition] until it holds; fails naming [what] after [timeout]. */
    fun until(
        what: String,
        timeout: Duration = TIMEOUT,
        condition: () -> Boolean,
    ) {
        value(what, timeout) { if (condition()) Unit else null }
    }

    /** Polls [read] until it returns a value and returns it; fails naming [what] after [timeout]. */
    fun <T : Any> value(
        what: String,
        timeout: Duration = TIMEOUT,
        read: () -> T?,
    ): T {
        val deadline = Instant.now() + timeout
        while (true) {
            read()?.let { return it }
            if (Instant.now() >= deadline) fail("no $what within $timeout")
            Thread.sleep(POLL.toMillis())
        }
    }
}
