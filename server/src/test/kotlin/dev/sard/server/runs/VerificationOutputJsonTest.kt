// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.runs

import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import java.time.Instant
import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertEquals

/** A verification's output and task survive their JSON columns as they were (run_steps.output, verify_task). */
@MutFlowTest
class VerificationOutputJsonTest {
    @Test
    fun `a verification output reads back from its stored JSON`() {
        val output =
            StepOutput.Verify(
                "snap",
                emptyList(),
                listOf(
                    StepOutput.Stage(1, "check", StageState.PASSED, Instant.EPOCH, Instant.EPOCH, 0, "ok", ""),
                    StepOutput.Stage(2, "restore", null, null, null, 2, "", "sha256:x"),
                ),
                VerifierVerdict.FAILED,
                listOf(StepOutput.Mismatch("tables", "12", "0")),
                1_500,
            )

        assertEquals(output, MutFlow.underTest { VerificationRuns.outputOf(output.json()) })
    }

    @Test
    fun `an output without verdict or restore time reads back without them, another kind or none as null`() {
        val bare = StepOutput.Verify("snap", emptyList())

        assertEquals(bare, MutFlow.underTest { VerificationRuns.outputOf(bare.json()) })
        assertEquals(null, MutFlow.underTest { VerificationRuns.outputOf(StepOutput.Run(0).json()) })
        assertEquals(null, MutFlow.underTest { VerificationRuns.outputOf(null) })
    }

    @Test
    fun `a verify task reads back from its stored JSON, with or without a fingerprint and source machines`() {
        val machines = listOf("1".repeat(64), "2".repeat(64))
        val tasks =
            listOf(
                VerifyTask(UUID(1, 2), "r", byteArrayOf(0, 7, -1), "f".repeat(64), Instant.EPOCH, 60, machines),
                VerifyTask(UUID(1, 2), "a".repeat(64), null, null, Instant.EPOCH, 21_600),
            )
        for (task in tasks) {
            assertEquals(task, MutFlow.underTest { VerifyTask.of(task.json()) })
        }
        assertEquals(null, MutFlow.underTest { VerifyTask.of(null) })
    }

    @Test
    fun `a verify task stored before V5 reads back without source machines`() {
        val stored =
            """{"jobId":"00000000-0000-0001-0000-000000000002","repositoryId":"r","fingerprint":null,""" +
                """"fingerprintSha256":null,"deadline":"1970-01-01T00:00:00Z","timeoutSeconds":60}"""

        val task = MutFlow.underTest { VerifyTask.of(stored) }

        assertEquals(VerifyTask(UUID(1, 2), "r", null, null, Instant.EPOCH, 60), task)
        assertEquals(emptyList(), task?.sourceMachineIds)
    }

    @Test
    fun `verify tasks that differ only in their source machines are not equal`() {
        val one = VerifyTask(UUID(1, 2), "r", null, null, Instant.EPOCH, 60, listOf("1".repeat(64)))
        val other = VerifyTask(UUID(1, 2), "r", null, null, Instant.EPOCH, 60, listOf("2".repeat(64)))

        assertEquals(false, MutFlow.underTest { one == other })
    }
}
