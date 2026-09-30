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
    fun `run states are the six of the schema, three of them active`() {
        val stored = MutFlow.underTest { RunState.entries.map { it.stored to it.active } }
        val expected =
            listOf(
                "queued" to true,
                "dispatched" to true,
                "running" to true,
                "succeeded" to false,
                "failed" to false,
                "cancelled" to false,
            )
        assertEquals(expected, stored)
    }

    @Test
    fun `every run state reads back from its stored value`() {
        for (state in RunState.entries) assertEquals(state, MutFlow.underTest { RunState.of(state.stored) })
    }

    @Test
    fun `step states are the nine of the schema and read back`() {
        val expected =
            listOf("queued", "dispatched", "running", "succeeded", "failed", "cancelled") +
                listOf("timed_out", "rejected", "lost")
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
            )
        for ((step, run) in expected) assertEquals(run, MutFlow.underTest { RunState.following(step) }, "$step")
    }

    @Test
    fun `triggers and actions are the schema's and read back`() {
        val triggers = MutFlow.underTest { Trigger.entries.map { it.stored } }
        assertEquals(listOf("schedule", "manual", "verification"), triggers)
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
            StepView(id, 0, Action.BACKUP, StepState.QUEUED, id, id, "postgresql", "main", CONFIG, RUNS_NOW, null)
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
