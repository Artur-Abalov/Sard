// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.notify

import dev.sard.server.runs.RunState
import dev.sard.server.runs.StepState
import dev.sard.server.runs.Trigger
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import java.time.Instant
import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNull

private val CLOSING_RUN: UUID = UUID.fromString("0192f0c4-7a10-7000-8000-00000000b001")
private val FINISHED_AT: Instant = Instant.parse("2026-10-07T03:12:00Z")

private fun closing(
    trigger: Trigger,
    previous: RunState?,
) = RunNotice(
    tenantId = UUID.randomUUID(),
    runId = CLOSING_RUN,
    trigger = trigger,
    status = RunState.SUCCEEDED,
    message = null,
    queuedAt = FINISHED_AT,
    finishedAt = FINISHED_AT,
    sourceId = UUID.randomUUID(),
    sourceName = "db-main",
    agentId = UUID.randomUUID(),
    agentHostname = "db1",
    stepStatus = StepState.SUCCEEDED,
    startedAt = FINISHED_AT,
    backup = null,
    previousStatus = previous,
)

private fun overdue(closedBy: RunNotice? = null) =
    OverdueNotice(
        tenantId = UUID.randomUUID(),
        episodeId = UUID.randomUUID(),
        sourceId = UUID.randomUUID(),
        sourceName = "db-main",
        agentHostname = "db1",
        countedFrom = Instant.parse("2026-10-05T03:10:00Z"),
        deadline = Instant.parse("2026-10-06T15:10:00Z"),
        closedBy = closedBy,
    )

private fun Message?.text() = this?.parts?.joinToString("") { it.text }

/** F6: the wording of an overdue episode in English, and when its end is left to the run that ended it. */
@MutFlowTest(includeTargets = [OverdueFormatter::class, RunNoticeFormatter::class])
class OverdueNoticeFormatterTest {
    private val formatter = RunNoticeFormatter(NoticeLanguage.EN, ConsoleUrl.parse("https://sard.example.com"))

    @Test
    fun `An opened episode names the last success and the deadline`() {
        val expected =
            """
            ⚠️ No successful backup for too long: db-main
            Agent: db1
            Last success: 2026-10-05 03:10 UTC
            Expected by: 2026-10-06 15:10 UTC
            """.trimIndent()
        assertEquals(expected, MutFlow.underTest { formatter.format(overdue()) }.text())
    }

    @Test
    fun `A source that never succeeded says so`() {
        val never = overdue().copy(countedFrom = null)
        assertEquals(
            "There has been no successful backup yet",
            MutFlow
                .underTest { formatter.format(never) }
                .text()
                ?.lines()
                ?.get(2),
        )
    }

    @Test
    fun `A quiet scheduled success tells that backups are back`() {
        val expected =
            """
            ✅ Backups are back: db-main
            Agent: db1
            Run: $CLOSING_RUN
            https://sard.example.com/runs/$CLOSING_RUN
            """.trimIndent()
        val resumed = overdue(closing(Trigger.SCHEDULE, RunState.SUCCEEDED))
        assertEquals(expected, MutFlow.underTest { formatter.format(resumed) }.text())
    }

    @Test
    fun `A success told about itself leaves the episode's end silent`() {
        for (run in listOf(closing(Trigger.MANUAL, null), closing(Trigger.CATCH_UP, RunState.FAILED))) {
            assertNull(MutFlow.underTest { formatter.format(overdue(run)) }, "${run.trigger}")
        }
    }

    @Test
    fun `Without a console address the end has no link`() {
        val plain = RunNoticeFormatter(NoticeLanguage.EN, null)
        val text = MutFlow.underTest { plain.format(overdue(closing(Trigger.SCHEDULE, null))) }.text()
        assertEquals(3, text?.lines()?.size)
    }
}
