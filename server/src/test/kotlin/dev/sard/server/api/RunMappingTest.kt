// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import dev.sard.server.runs.Action
import dev.sard.server.runs.BackupResult
import dev.sard.server.runs.RunState
import dev.sard.server.runs.RunView
import dev.sard.server.runs.StepState
import dev.sard.server.runs.StepView
import dev.sard.server.runs.Trigger
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
        phase = "uploading",
        bytesProcessed = 1,
        bytesTotal = 2,
        message = "m",
        startedAt = AT,
        finishedAt = null,
        filesProcessed = 3,
        filesTotal = 4,
        backup = BackupResult("snap", 10, 5, "repo", partial = true),
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
                        RunMapping.run(run(status))
                    }.status.name,
            )
        }
        for (trigger in Trigger.entries) {
            assertEquals(
                trigger.name,
                MutFlow.underTest { RunMapping.run(run(trigger = trigger)) }.trigger.name,
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
        assertEquals(BackupOutput("snap", 10, 5, "repo", partial = true), mapped.backup)
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
        assertEquals(1, MutFlow.underTest { RunMapping.run(run()) }.steps.size)
        val summary = MutFlow.underTest { RunMapping.summary(run()) }
        assertEquals(listOf(ID, ID, ID, "m"), listOf(summary.id, summary.sourceId, summary.agentId, summary.message))
        assertEquals(listOf(AT, AT, null), listOf(summary.queuedAt, summary.startedAt, summary.finishedAt))
    }

    @Test
    fun `a run and its summary name their source and say whether it was deleted`() {
        val full = MutFlow.underTest { RunMapping.run(run()) }
        val summary = MutFlow.underTest { RunMapping.summary(run()) }

        assertEquals(listOf("etc", true), listOf(full.sourceName, full.sourceDeleted))
        assertEquals(listOf("etc", true), listOf(summary.sourceName, summary.sourceDeleted))
    }
}
