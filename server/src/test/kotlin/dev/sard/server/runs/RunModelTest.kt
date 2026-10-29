// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.runs

import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith

/** Stored values are the schema's CHECK lists (V202609301200) and read back to the same constant. */
@MutFlowTest
class RunModelTest {
    @Test
    fun `run states are the seven of the schema, three of them active`() {
        val stored = MutFlow.underTest { RunState.entries.map { it.stored to it.active } }
        val expected =
            listOf(
                "queued" to true,
                "dispatched" to true,
                "running" to true,
                "succeeded" to false,
                "failed" to false,
                "cancelled" to false,
                "agent_unavailable" to false,
            )
        assertEquals(expected, stored)
    }

    private val at = java.time.Instant.parse("2026-10-27T12:00:00Z")

    private fun cancel(result: CancelResult) = Cancellation(at, "admin", result)

    @Test
    fun `the outcome of a run is told by its status, its step and its cancel`() {
        val pending = cancel(CancelResult.PENDING)
        val cases =
            listOf(
                Triple(RunState.QUEUED, null, null) to RunOutcome.QUEUED,
                Triple(RunState.DISPATCHED, StepState.DISPATCHED, null) to RunOutcome.DISPATCHED,
                Triple(RunState.RUNNING, StepState.RUNNING, null) to RunOutcome.RUNNING,
                Triple(RunState.DISPATCHED, StepState.DISPATCHED, pending) to RunOutcome.CANCELLING,
                Triple(RunState.RUNNING, StepState.RUNNING, pending) to RunOutcome.CANCELLING,
                Triple(RunState.SUCCEEDED, StepState.SUCCEEDED, null) to RunOutcome.SUCCEEDED,
                Triple(RunState.SUCCEEDED, StepState.SUCCEEDED, cancel(CancelResult.FINISHED_FIRST)) to
                    RunOutcome.SUCCEEDED,
                Triple(RunState.FAILED, StepState.FAILED, null) to RunOutcome.FAILED,
                Triple(RunState.FAILED, null, null) to RunOutcome.FAILED,
                Triple(RunState.FAILED, StepState.TIMED_OUT, null) to RunOutcome.TIMED_OUT,
                Triple(RunState.FAILED, StepState.REJECTED, null) to RunOutcome.REJECTED,
                Triple(RunState.FAILED, StepState.LOST, null) to RunOutcome.LOST,
                Triple(RunState.CANCELLED, StepState.CANCELLED, null) to RunOutcome.CANCELLED,
                Triple(RunState.CANCELLED, StepState.CANCELLED, cancel(CancelResult.CANCELLED)) to
                    RunOutcome.CANCELLED,
                Triple(RunState.CANCELLED, StepState.CANCELLED, cancel(CancelResult.UNCONFIRMED)) to
                    RunOutcome.CANCEL_UNCONFIRMED,
                Triple(RunState.AGENT_UNAVAILABLE, StepState.AGENT_UNAVAILABLE, null) to RunOutcome.AGENT_UNAVAILABLE,
            )
        for ((input, expected) in cases) {
            val (status, step, cancellation) = input
            assertEquals(expected, MutFlow.underTest { RunOutcome.of(status, step, cancellation) }, "$input")
        }
    }

    // J1c: Запуск онлайн-агента в очереди не называется ждущим
    @Test
    fun `a queued run is waiting for the agent only while its agent is offline`() {
        assertEquals(RunOutcome.QUEUED, MutFlow.underTest { RunOutcome.of(RunState.QUEUED, null, null, true) })
        assertEquals(
            RunOutcome.WAITING_FOR_AGENT,
            MutFlow.underTest { RunOutcome.of(RunState.QUEUED, null, null, false) },
        )
        assertEquals(
            RunOutcome.DISPATCHED,
            MutFlow.underTest { RunOutcome.of(RunState.DISPATCHED, StepState.DISPATCHED, null, false) },
        )
    }

    @Test
    fun `every cancel result reads back from its stored value`() {
        for (result in CancelResult.entries) assertEquals(result, MutFlow.underTest { CancelResult.of(result.stored) })
    }

    @Test
    fun `every run state reads back from its stored value`() {
        for (state in RunState.entries) assertEquals(state, MutFlow.underTest { RunState.of(state.stored) })
    }

    @Test
    fun `step states are the ten of the schema and read back`() {
        val expected =
            listOf("queued", "dispatched", "running", "succeeded", "failed", "cancelled") +
                listOf("timed_out", "rejected", "lost", "agent_unavailable")
        assertEquals(expected, MutFlow.underTest { StepState.entries.map { it.stored } })
        for (state in StepState.entries) assertEquals(state, MutFlow.underTest { StepState.of(state.stored) })
    }

    @Test
    fun `a step is active while queued, dispatched or running`() {
        val active = MutFlow.underTest { StepState.entries.filter { it.active } }
        assertEquals(listOf(StepState.QUEUED, StepState.DISPATCHED, StepState.RUNNING), active)
    }

    @Test
    fun `a run follows its single step, and every failure of the step fails the run`() {
        val expected =
            mapOf(
                StepState.QUEUED to RunState.QUEUED,
                StepState.DISPATCHED to RunState.DISPATCHED,
                StepState.RUNNING to RunState.RUNNING,
                StepState.SUCCEEDED to RunState.SUCCEEDED,
                StepState.FAILED to RunState.FAILED,
                StepState.CANCELLED to RunState.CANCELLED,
                StepState.TIMED_OUT to RunState.FAILED,
                StepState.REJECTED to RunState.FAILED,
                StepState.LOST to RunState.FAILED,
                StepState.AGENT_UNAVAILABLE to RunState.AGENT_UNAVAILABLE,
            )
        for ((step, run) in expected) assertEquals(run, MutFlow.underTest { RunState.following(step) }, "$step")
    }

    @Test
    fun `triggers and actions are the schema's and read back`() {
        val triggers = MutFlow.underTest { Trigger.entries.map { it.stored } }
        assertEquals(listOf("schedule", "manual", "verification", "catch_up"), triggers)
        val actions = MutFlow.underTest { Action.entries.map { it.stored } }
        assertEquals(listOf("backup", "restore", "verify", "run"), actions)
        for (trigger in Trigger.entries) assertEquals(trigger, MutFlow.underTest { Trigger.of(trigger.stored) })
        for (action in Action.entries) assertEquals(action, MutFlow.underTest { Action.of(action.stored) })
    }

    @Test
    fun `an unknown stored value is an error, not a guess`() {
        assertFailsWith<NoSuchElementException> { MutFlow.underTest { RunState.of("lost") } }
        assertFailsWith<NoSuchElementException> { MutFlow.underTest { StepState.of("paused") } }
        assertFailsWith<NoSuchElementException> { MutFlow.underTest { Trigger.of("cron") } }
        assertFailsWith<NoSuchElementException> { MutFlow.underTest { Action.of("dump") } }
    }

    @Test
    fun `drafts and views never print the config`() {
        val draft = SourceDraft("prod-db", java.util.UUID(0, 1), "postgresql", "main", CONFIG)
        val text = MutFlow.underTest { draft.toString() }
        assertEquals(
            "SourceDraft(name=prod-db, agentId=00000000-0000-0000-0000-000000000001, plugin=postgresql, " +
                "repositoryName=main)",
            text,
        )
        val id = java.util.UUID(0, 1)
        val source = SourceView(id, "prod-db", id, "postgresql", "main", CONFIG, RUNS_NOW, RUNS_NOW)
        val step =
            StepView(
                id,
                0,
                Action.BACKUP,
                StepState.QUEUED,
                id,
                id,
                "postgresql",
                "main",
                CONFIG,
                RUNS_NOW,
                null,
                runId = id,
            )
        val records =
            listOf(
                dev.sard.server.persistence
                    .SourceRecord(id, id, "prod-db", "postgresql", CONFIG, "main", RUNS_NOW, RUNS_NOW),
                dev.sard.server.persistence
                    .RunStepRecord(id, id, 0, id, id, "postgresql", "backup", "main", CONFIG, "queued", RUNS_NOW),
            )
        for (printed in listOf(source, step, *records.toTypedArray()).map { MutFlow.underTest { it.toString() } }) {
            assertEquals(false, "pg-prod" in printed, printed)
        }
    }
}
