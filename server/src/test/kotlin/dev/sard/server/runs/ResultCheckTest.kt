// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.runs

import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import kotlin.test.Test
import kotlin.test.assertEquals

private const val REPOSITORY_ID = "5f0c3e2d9a8b7c6d5e4f3a2b1c0d9e8f7a6b5c4d3e2f1a0b9c8d7e6f5a4b3c2d"
private val BACKUP = StepOutput.Backup("4a3b2c1d", 1_000, 100, REPOSITORY_ID)
private val RESTORED =
    StepOutput.Restore("/var/lib/sard-agent/restore/etc", filesRestored = 2, bytesRestored = 4, ownersRestored = false)
private val LEFTOVER = StepOutput.Restore(target = "", leftover = "/var/lib/sard-agent/restore/.sard-staging/c1")

/** A result is checked against the step's action before it is recorded (S7a, answer 5). */
@MutFlowTest
class ResultCheckTest {
    private fun check(
        action: Action,
        status: StepState?,
        output: StepOutput?,
        message: String? = null,
    ) = MutFlow.underTest { ResultCheck.of(action, StepReport(status, message, output)) }

    @Test
    fun `a succeeded backup with its snapshot is kept as it is`() {
        val verdict = check(Action.BACKUP, StepState.SUCCEEDED, BACKUP)

        assertEquals(Verdict(StepOutcome(StepState.SUCCEEDED, null), BACKUP, invalid = null), verdict)
    }

    @Test
    fun `a backup output is partial exactly when its result failed`() {
        val failed = check(Action.BACKUP, StepState.FAILED, BACKUP, "2 files unreadable")
        val succeeded = check(Action.BACKUP, StepState.SUCCEEDED, BACKUP)

        assertEquals(BACKUP.copy(partial = true), failed.output)
        assertEquals(BACKUP.copy(partial = false), succeeded.output)
    }

    @Test
    fun `a backup that added nothing, of nothing, is still a snapshot`() {
        val empty = BACKUP.copy(totalBytes = 0, addedBytes = 0)

        assertEquals(empty, check(Action.BACKUP, StepState.SUCCEEDED, empty).output)
    }

    @Test
    fun `a failure keeps the agent's status and message`() {
        for (status in listOf(StepState.FAILED, StepState.CANCELLED, StepState.TIMED_OUT, StepState.REJECTED)) {
            val verdict = check(Action.BACKUP, status, null, "unknown plugin \"absent\"")

            assertEquals(Verdict(StepOutcome(status, "unknown plugin \"absent\""), null, invalid = null), verdict)
        }
    }

    @Test
    fun `each action keeps the output of its own kind`() {
        val outputs =
            mapOf(
                Action.RESTORE to StepOutput.Restore("/var/lib/sard-agent/restore/1"),
                Action.VERIFY to StepOutput.Verify("4a3b2c1d", listOf(StepOutput.Check("read-data", true, ""))),
                Action.RUN to StepOutput.Run(0),
            )
        for ((action, output) in outputs) {
            assertEquals(output, check(action, StepState.SUCCEEDED, output).output)
        }
    }

    @Test
    fun `a result without a final status is an invalid result`() {
        for (status in listOf(null, StepState.QUEUED, StepState.DISPATCHED, StepState.RUNNING, StepState.LOST)) {
            val verdict = check(Action.BACKUP, status, BACKUP)

            val outcome = StepOutcome(StepState.FAILED, "invalid result: no final status")
            assertEquals(Verdict(outcome, null, invalid = "no final status"), verdict)
        }
    }

    @Test
    fun `a succeeded backup without its snapshot is an invalid result`() {
        val broken =
            mapOf(
                null to "a succeeded backup without its output",
                BACKUP.copy(snapshotId = "") to "a backup output without a snapshot id",
                BACKUP.copy(repositoryId = "") to "a backup output without a repository id",
                BACKUP.copy(totalBytes = -1) to "a backup output with negative sizes",
                BACKUP.copy(addedBytes = -1) to "a backup output with negative sizes",
            )
        for ((output, problem) in broken) {
            val verdict = check(Action.BACKUP, StepState.SUCCEEDED, output)

            assertEquals(Verdict(StepOutcome(StepState.FAILED, "invalid result: $problem"), null, problem), verdict)
        }
    }

    @Test
    fun `a succeeded result with the output of another action is an invalid result`() {
        val verdict = check(Action.RESTORE, StepState.SUCCEEDED, BACKUP)

        val problem = "a backup output for a restore step"
        assertEquals(Verdict(StepOutcome(StepState.FAILED, "invalid result: $problem"), null, problem), verdict)
    }

    @Test
    fun `a failure drops an output it cannot carry and keeps its status`() {
        val wrong = check(Action.RESTORE, StepState.FAILED, BACKUP, "disk full")
        val broken = check(Action.BACKUP, StepState.FAILED, BACKUP.copy(snapshotId = ""), "disk full")

        for (verdict in listOf(wrong, broken)) {
            assertEquals(Verdict(StepOutcome(StepState.FAILED, "disk full"), null, invalid = null), verdict)
        }
    }

    @Test
    fun `a succeeded step other than a backup may come without output`() {
        for (action in listOf(Action.RESTORE, Action.VERIFY, Action.RUN)) {
            val verdict = check(action, StepState.SUCCEEDED, null)

            assertEquals(Verdict(StepOutcome(StepState.SUCCEEDED, null), null, invalid = null), verdict)
        }
    }

    @Test
    fun `the output is stored as JSON named by its kind`() {
        val check = StepOutput.Check("read-data", false, "pack 1f: \"bad\"")
        val stored =
            MutFlow.underTest {
                listOf(BACKUP, StepOutput.Restore("/r"), StepOutput.Verify("4a", listOf(check)), StepOutput.Run(-1))
                    .map { it.json() }
            }

        val expected =
            listOf(
                """{"kind":"backup","snapshotId":"4a3b2c1d","totalBytes":1000,"addedBytes":100,""" +
                    """"repositoryId":"$REPOSITORY_ID","partial":false}""",
                """{"kind":"restore","target":"/r","filesRestored":0,"bytesRestored":0,"database":"",""" +
                    """"tablesRestored":0,"rolesRestored":0,"ownersRestored":false,"leftover":""}""",
                """{"kind":"verify","snapshotId":"4a","checks":[{"name":"read-data","passed":false,""" +
                    """"detail":"pack 1f: \"bad\""}],"stages":[],"verdict":null,"mismatches":[],""" +
                    """"restoreMillis":null}""",
                """{"kind":"run","exitCode":-1}""",
            )
        assertEquals(expected, stored)
    }

    // --- R2a: the output of a restore (all or nothing: a failure carries only what the rollback left)

    @Test
    fun `a succeeded restore keeps its output`() {
        assertEquals(RESTORED, check(Action.RESTORE, StepState.SUCCEEDED, RESTORED).output)
    }

    @Test
    fun `a failed restore whose rollback failed keeps what is left, whatever the status`() {
        for (status in listOf(StepState.FAILED, StepState.CANCELLED, StepState.TIMED_OUT, StepState.REJECTED)) {
            val verdict = check(Action.RESTORE, status, LEFTOVER, "CLEANUP_FAILED (rollback): boom")

            val outcome = StepOutcome(status, "CLEANUP_FAILED (rollback): boom")
            assertEquals(Verdict(outcome, LEFTOVER, invalid = null), verdict, status.name)
        }
    }

    @Test
    fun `a failed restore with the fields of a result is an invalid result`() {
        for (status in listOf(StepState.FAILED, StepState.CANCELLED, StepState.TIMED_OUT)) {
            val verdict = check(Action.RESTORE, status, RESTORED, "no space")

            val problem = "a restore output with a result for a restore that did not succeed"
            assertEquals(Verdict(StepOutcome(StepState.FAILED, "invalid result: $problem"), null, problem), verdict)
        }
    }

    @Test
    fun `a failed restore with leftover and result fields together is an invalid result`() {
        val mixed = LEFTOVER.copy(target = "/t", filesRestored = 1)

        val verdict = check(Action.RESTORE, StepState.FAILED, mixed, "boom")

        assertEquals(StepState.FAILED, verdict.outcome.status)
        assertEquals(
            "invalid result: a restore output with a result for a restore that did not succeed",
            verdict.outcome.message,
        )
    }

    @Test
    fun `a failed restore with an empty output carries nothing`() {
        val verdict = check(Action.RESTORE, StepState.FAILED, StepOutput.Restore(target = ""), "boom")

        assertEquals(Verdict(StepOutcome(StepState.FAILED, "boom"), null, invalid = null), verdict)
    }

    @Test
    fun `a succeeded restore with leftover or negative sizes is an invalid result`() {
        val broken =
            mapOf(
                RESTORED.copy(leftover = "/x") to "a succeeded restore with something left behind",
                RESTORED.copy(filesRestored = -1) to "a restore output with negative sizes",
                RESTORED.copy(bytesRestored = -1) to "a restore output with negative sizes",
                RESTORED.copy(tablesRestored = -1) to "a restore output with negative sizes",
                RESTORED.copy(rolesRestored = -1) to "a restore output with negative sizes",
            )
        for ((output, problem) in broken) {
            val verdict = check(Action.RESTORE, StepState.SUCCEEDED, output)

            assertEquals(Verdict(StepOutcome(StepState.FAILED, "invalid result: $problem"), null, problem), verdict)
        }
    }

    @Test
    fun `a backup output for a restore step that failed is dropped, not an invalid result`() {
        val verdict = check(Action.RESTORE, StepState.FAILED, BACKUP, "boom")

        assertEquals(Verdict(StepOutcome(StepState.FAILED, "boom"), null, invalid = null), verdict)
    }

    @Test
    fun `the output of a restore is stored with every field`() {
        val stored = MutFlow.underTest { RESTORED.json() }

        val expected =
            """{"kind":"restore","target":"/var/lib/sard-agent/restore/etc","filesRestored":2,"bytesRestored":4,""" +
                """"database":"","tablesRestored":0,"rolesRestored":0,"ownersRestored":false,"leftover":""}"""
        assertEquals(expected, stored)
    }
}

private fun hexOf(bytes: ByteArray) =
    java.security.MessageDigest
        .getInstance("SHA-256")
        .digest(bytes)
        .joinToString("") { "%02x".format(it) }

private val FP_BYTES = """{"facts":[{"name":"tables","rule":"exact","value":"3"}],"format":1,"plugin":"postgresql"}""".toByteArray()

/** The fingerprint a backup brings is checked with it (V2): a hash that is not that of the bytes fails the step. */
@MutFlowTest
class FingerprintResultCheckTest {
    private fun check(output: StepOutput.Backup) =
        MutFlow.underTest { ResultCheck.of(Action.BACKUP, StepReport(StepState.SUCCEEDED, null, output)) }

    @Test
    fun `a backup with its fingerprint and number of files is kept`() {
        val backup = BACKUP.copy(fingerprint = SourceFingerprint(FP_BYTES, hexOf(FP_BYTES)), totalFiles = 1)

        assertEquals(Verdict(StepOutcome(StepState.SUCCEEDED, null), backup, invalid = null), check(backup))
    }

    @Test
    fun `a fingerprint whose hash is not that of its bytes is an invalid result`() {
        val backup = BACKUP.copy(fingerprint = SourceFingerprint(FP_BYTES, "0".repeat(64)), totalFiles = 1)

        val problem = "a fingerprint whose hash is not that of its bytes"
        assertEquals(Verdict(StepOutcome(StepState.FAILED, "invalid result: $problem"), null, problem), check(backup))
    }

    @Test
    fun `a negative number of files is an invalid result`() {
        assertEquals("a backup output with negative sizes", check(BACKUP.copy(totalFiles = -1)).invalid)
    }

    @Test
    fun `the stored output has the summary of the fingerprint, the number of files and the warnings`() {
        val backup =
            BACKUP.copy(
                fingerprint = SourceFingerprint(FP_BYTES, hexOf(FP_BYTES)),
                totalFiles = 1,
                warnings = listOf("w"),
            )
        val json = MutFlow.underTest { backup.json() }

        val expected =
            """{"kind":"backup","snapshotId":"4a3b2c1d","totalBytes":1000,"addedBytes":100,"repositoryId":"$REPOSITORY_ID",""" +
                """"partial":false,"totalFiles":1,"fingerprint":{"sha256":"${hexOf(FP_BYTES)}","format":1,""" +
                """"plugin":"postgresql","facts":[{"name":"tables","rule":"exact","value":"3"}],"hidden":0},"warnings":["w"]}"""
        assertEquals(expected, json)
        assertEquals(SnapshotTotals(1, 1_000), backup.totals())
        assertEquals(null, BACKUP.totals())
    }
}
