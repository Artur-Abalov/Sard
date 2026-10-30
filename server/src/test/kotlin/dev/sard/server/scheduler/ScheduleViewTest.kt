// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.scheduler

import java.time.Instant
import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertEquals

class ScheduleViewTest {
    private fun view(
        enabled: Boolean,
        notifyOnSuccess: Boolean,
    ): ScheduleView {
        val at = Instant.parse("2026-10-30T03:00:00Z")
        return ScheduleView(
            UUID.randomUUID(),
            UUID.randomUUID(),
            "0 3 * * *",
            "Europe/Moscow",
            enabled,
            at,
            null,
            null,
            0,
            notifyOnSuccess,
            null,
            at,
            at,
        )
    }

    @Test
    fun `the draft of a schedule is what a caller sets`() {
        assertEquals(ScheduleDraft("0 3 * * *", "Europe/Moscow", true, false), view(true, false).draft)
        assertEquals(ScheduleDraft("0 3 * * *", "Europe/Moscow", false, true), view(false, true).draft)
    }
}
