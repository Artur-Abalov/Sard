// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import dev.sard.server.runs.Action
import dev.sard.server.runs.BackupResult
import dev.sard.server.runs.FingerprintSummary
import dev.sard.server.runs.FingerprintFact as DomainFact
import dev.sard.server.runs.RestoreInfo
import dev.sard.server.runs.RestoreResult
import dev.sard.server.runs.RunState
import dev.sard.server.runs.RunView
import dev.sard.server.runs.StepState
import dev.sard.server.runs.StepView
import dev.sard.server.runs.Trigger
import dev.sard.server.scheduler.CatchUpPeriod
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import java.time.Instant
import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNull

private val AT = Instant.parse("2026-10-01T12:00:00Z")
private val ID = UUID.fromString("0192f7a0-0000-7000-8000-000000000001")

/** The domain's runs and steps as the contract names them. */
@MutFlowTest
class RunMappingTest {
    private fun step(
        status: StepState = StepState.RUNNING,
        action: Action = Action.BACKUP,
    ) = StepView(
        ID,
        0,
        action,
        status,
        ID,
        ID,
        "files",
        "qa",
        "{}",
        AT,
        AT,
        runId = ID,
        phase = "uploading",
        bytesProcessed = 1,
        bytesTotal = 2,
        message = "m",
        startedAt = AT,
        finishedAt = null,
        filesProcessed = 3,
        filesTotal = 4,
        backup =
            BackupResult(
                "snap",
                10,
                5,
                "repo",
                partial = true,
                totalFiles = 7,
                fingerprint =
                    FingerprintSummary("f".repeat(64), 1, "postgresql", listOf(DomainFact("tables", "exact", "2")), 0),
                warnings = listOf("w"),
            ),
    )

    private fun run(
        status: RunState = RunState.RUNNING,
        trigger: Trigger = Trigger.MANUAL,
    ) = RunView(ID, ID, "etc", true, ID, trigger, status, "m", AT, AT, null, listOf(step()))

    @Test
    fun `every status, trigger and action of the domain has its name in the contract`() {
        for (status in StepState.entries) {
            assertEquals(
                status.name,
                MutFlow
                    .underTest {
                        RunMapping.step(step(status))
                    }.status.name,
            )
        }
        for (status in RunState.entries) {
            assertEquals(
                status.name,
                MutFlow
                    .underTest {
                        RunMapping.run(run(status), null)
                    }.status.name,
            )
        }
        for (trigger in Trigger.entries) {
            assertEquals(
                trigger.name,
                MutFlow.underTest { RunMapping.run(run(trigger = trigger), null) }.trigger.name,
            )
        }
        for (action in Action.entries) {
            assertEquals(
                action.name,
                MutFlow
                    .underTest {
                        RunMapping.step(step(action = action))
                    }.action.name,
            )
        }
    }

    @Test
    fun `every status a client filters by is a state of the domain`() {
        for (status in RunStatus.entries) assertEquals(status.name, MutFlow.underTest { RunMapping.state(status) }.name)
    }

    @Test
    fun `a step carries every field of the domain's`() {
        val mapped = MutFlow.underTest { RunMapping.step(step()) }

        assertEquals(StepPhase.UPLOADING, mapped.phase)
        assertEquals(
            listOf(1L, 2L, 3L, 4L),
            listOf(mapped.bytesProcessed, mapped.bytesTotal, mapped.filesProcessed, mapped.filesTotal),
        )
        val fingerprint =
            Fingerprint("f".repeat(64), 1, "postgresql", listOf(FingerprintFact("tables", "exact", "2")), 0)
        assertEquals(
            BackupOutput("snap", 10, 5, "repo", partial = true, restorable = false, 7, fingerprint, listOf("w")),
            mapped.backup,
        )
        assertEquals(listOf("files", "qa", "m"), listOf(mapped.plugin, mapped.repositoryName, mapped.message))
        assertEquals(
            listOf(AT, AT, AT, null),
            listOf(mapped.queuedAt, mapped.dispatchedAt, mapped.startedAt, mapped.finishedAt),
        )
    }

    @Test
    fun `a step without a phase or a backup shows none`() {
        val mapped = MutFlow.underTest { RunMapping.step(step().copy(phase = null, backup = null)) }

        assertNull(mapped.phase)
        assertNull(mapped.backup)
    }

    @Test
    fun `a run has its steps, a summary has none`() {
        assertEquals(1, MutFlow.underTest { RunMapping.run(run(), null) }.steps.size)
        val summary = MutFlow.underTest { RunMapping.summary(run(), null) }
        assertEquals(listOf(ID, ID, ID, "m"), listOf(summary.id, summary.sourceId, summary.agentId, summary.message))
        assertEquals(listOf(AT, AT, null), listOf(summary.queuedAt, summary.startedAt, summary.finishedAt))
    }

    @Test
    fun `a run and its summary name their source and say whether it was deleted`() {
        val full = MutFlow.underTest { RunMapping.run(run(), null) }
        val summary = MutFlow.underTest { RunMapping.summary(run(), null) }

        assertEquals(listOf("etc", true), listOf(full.sourceName, full.sourceDeleted))
        assertEquals(listOf("etc", true), listOf(summary.sourceName, summary.sourceDeleted))
    }

    @Test
    fun `a catch-up period is named by a run and by its summary, as it is, capped or not`() {
        val until = AT.plusSeconds(3600)
        for (capped in listOf(true, false)) {
            val period = CatchUpPeriod(AT, until, 12, capped, "Europe/Berlin")
            val expected = CatchUp(AT, until, 12, capped, "Europe/Berlin")
            assertEquals(expected, MutFlow.underTest { RunMapping.run(run(), period) }.catchUp)
            assertEquals(expected, MutFlow.underTest { RunMapping.summary(run(), period) }.catchUp)
        }
    }

    @Test
    fun `a run without a period names none`() {
        assertNull(MutFlow.underTest { RunMapping.run(run(), null) }.catchUp)
        assertNull(MutFlow.underTest { RunMapping.summary(run(), null) }.catchUp)
    }

    // --- R2a: the kind of a run, who asked, what a restore restores and what its step reported

    @Test
    fun `every kind of the domain has its name in the contract`() {
        for (kind in dev.sard.server.runs.RunKind.entries) {
            val view = run().copy(kind = kind)
            assertEquals(kind.name, MutFlow.underTest { RunMapping.run(view, null) }.kind.name)
            assertEquals(kind.name, MutFlow.underTest { RunMapping.summary(view, null) }.kind.name)
            assertEquals(kind.name, MutFlow.underTest { RunMapping.kindOf(RunKind.valueOf(kind.name)) }.name)
        }
    }

    @Test
    fun `a run and its summary say who started it`() {
        val view = run().copy(requestedBy = "admin")

        assertEquals("admin", MutFlow.underTest { RunMapping.run(view, null) }.requestedBy)
        assertEquals("admin", MutFlow.underTest { RunMapping.summary(view, null) }.requestedBy)
        assertNull(MutFlow.underTest { RunMapping.run(run(), null) }.requestedBy)
    }

    @Test
    fun `a restore run shows what it restores with its config as an object`() {
        val info = RestoreInfo("4f1c", AT, "copy", """{"destination":"etc/x","paths":["/etc"]}""", "/r/etc/x")

        val mapped = MutFlow.underTest { RunMapping.run(run().copy(restore = info), null) }.restore

        val config = mapOf("destination" to "etc/x", "paths" to listOf("/etc"))
        assertEquals(RunRestore("4f1c", AT, "copy", config, "/r/etc/x"), mapped)
        assertNull(MutFlow.underTest { RunMapping.run(run(), null) }.restore)
    }

    @Test
    fun `a step shows the output of its restore and whether a backup can be restored`() {
        val result = RestoreResult("/r/etc", 2, 4, null, null, null, false, null)

        val mapped =
            MutFlow.underTest {
                RunMapping.step(
                    step().copy(restore = result, backup = BackupResult("s", 1, 1, "r", restorable = true)),
                )
            }

        val expected = RestoreOutput("/r/etc", 2, 4, null, null, null, ownersRestored = false, leftover = null)
        assertEquals(expected, mapped.restore)
        assertEquals(true, mapped.backup?.restorable)
        assertNull(MutFlow.underTest { RunMapping.step(step()) }.restore)
    }

    // --- R2a: how a restore step ended is told by the server, not derived by the page

    private fun outcomeOf(
        status: StepState,
        result: RestoreResult? = null,
        action: Action = Action.RESTORE,
    ) = MutFlow.underTest { RunMapping.step(step(status, action).copy(restore = result)) }.restoreOutcome

    private val written = RestoreResult("/r/etc", 2, 4, null, null, null, false, null)

    @Test
    fun `a succeeded restore step with an output restored`() {
        assertEquals(RestoreOutcome.RESTORED, outcomeOf(StepState.SUCCEEDED, written))
        assertNull(outcomeOf(StepState.SUCCEEDED, null))
    }

    /** R2b: the outcome of a restore of a database is told like that of files. */
    @Test
    fun `a restore of a database ends as restored, nothing or a failed rollback`() {
        val database = RestoreResult("app_restored", null, null, "app_restored", 2, 0, null, null)
        val left = RestoreResult(null, null, null, null, null, null, null, "database app_restored")
        assertEquals(RestoreOutcome.RESTORED, outcomeOf(StepState.SUCCEEDED, database))
        assertEquals(RestoreOutcome.NOTHING, outcomeOf(StepState.FAILED, null))
        assertEquals(RestoreOutcome.ROLLBACK_FAILED, outcomeOf(StepState.FAILED, left))
        assertEquals(RestoreOutcome.LATE, outcomeOf(StepState.LOST, database))
        val finished = step(StepState.SUCCEEDED, Action.RESTORE).copy(restore = database)
        val mapped = MutFlow.underTest { RunMapping.step(finished) }
        assertEquals(RestoreOutput("app_restored", null, null, "app_restored", 2, 0, null, null), mapped.restore)
    }

    @Test
    fun `a restore step that did not succeed restored nothing`() {
        for (status in listOf(StepState.FAILED, StepState.REJECTED, StepState.TIMED_OUT, StepState.CANCELLED)) {
            assertEquals(RestoreOutcome.NOTHING, outcomeOf(status), status.name)
        }
    }

    @Test
    fun `a leftover is a failed rollback whatever the status`() {
        val left = RestoreResult(null, null, null, null, null, null, null, "database app_restored")
        for (status in StepState.entries) {
            assertEquals(RestoreOutcome.ROLLBACK_FAILED, outcomeOf(status, left), status.name)
        }
        assertEquals(RestoreOutcome.NOTHING, outcomeOf(StepState.FAILED, left.copy(leftover = "")))
    }

    @Test
    fun `a lost restore step is late only when its late output says where the data is`() {
        assertEquals(RestoreOutcome.LATE, outcomeOf(StepState.LOST, written))
        assertNull(outcomeOf(StepState.LOST, null))
        assertNull(outcomeOf(StepState.LOST, written.copy(target = null)))
        assertNull(outcomeOf(StepState.LOST, written.copy(target = "")))
    }

    @Test
    fun `a restore step that has not ended has no outcome`() {
        for (status in listOf(StepState.QUEUED, StepState.DISPATCHED, StepState.RUNNING)) {
            assertNull(outcomeOf(status), status.name)
        }
    }

    @Test
    fun `a step of another action has no restore outcome`() {
        for (action in Action.entries.filter { it != Action.RESTORE }) {
            assertNull(outcomeOf(StepState.FAILED, null, action), action.name)
        }
    }
}
