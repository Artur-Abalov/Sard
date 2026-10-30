// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.sample

import dev.sard.server.extension.RunEvent
import dev.sard.server.extension.SardEvent
import dev.sard.server.extension.SessionEvent
import dev.sard.server.extension.SettingsChanged
import org.springframework.context.event.EventListener
import org.springframework.jdbc.core.JdbcTemplate

private const val SLOW_MILLIS = 3000L

/**
 * Writes every event of the core as a row of sample_events (CORE1 Р5). `record` does that, `throw` fails on every
 * event (the core must not care), `slow` waits three seconds first (the core waits for it, synchronously, Р21).
 */
class SampleEventListener(
    private val jdbc: JdbcTemplate,
    private val mode: String,
) {
    @EventListener
    fun on(event: SardEvent) {
        when (mode) {
            "throw" -> error("the sample listener throws on ${event.type}")
            "slow" -> Thread.sleep(SLOW_MILLIS)
        }
        jdbc.update("insert into sample_events (type, detail) values (?, ?)", event.type, detailOf(event) + actorOf(event))
    }

    private fun actorOf(event: SardEvent) = " actor=" + (event.actor?.let { "${it.kind}:${it.id}" } ?: "none")

    /** The fields that identify the event, as text; never a secret, because an event carries none. */
    private fun detailOf(event: SardEvent): String =
        when (event) {
            is RunEvent -> "run=${event.runId} kind=${event.kind} trigger=${event.trigger}"
            is SettingsChanged -> "setting=${event.setting} object=${event.objectId}"
            is SessionEvent -> "address=${event.clientAddress}"
        }
}
