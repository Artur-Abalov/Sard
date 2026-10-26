// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents.dispatch

import com.google.protobuf.ByteString
import dev.sard.proto.agent.v1.RunStep
import dev.sard.server.runs.Action
import dev.sard.server.runs.VerifyTask
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import java.time.Duration
import java.time.Instant
import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertTrue
import dev.sard.proto.agent.v1.Action as ProtoAction
import dev.sard.proto.agent.v1.VerifyTask as ProtoVerifyTask

@MutFlowTest
class DispatchPartsTest {
    @Test
    fun `a step becomes the RunStep the agent executes, command_id being the step id`() {
        val expected =
            RunStep
                .newBuilder()
                .setCommandId(UUID(0, 1).toString())
                .setPlugin("postgresql")
                .setConfigJson("""{"n": 1}""")
                .setAction(ProtoAction.ACTION_BACKUP)
                .setRepositoryName("main")
                .putTags("sard.step", UUID(0, 1).toString())
                .putTags("sard.run", UUID(8, 1).toString())
                .putTags("sard.source", UUID(9, 1).toString())
                .build()
        assertEquals(expected, MutFlow.underTest { RunSteps.of(step(1)) })
    }

    @Test
    fun `a verify step carries its snapshot, its task and its timeout, and nothing else`() {
        val deadline = Instant.parse("2026-10-20T04:30:00Z")
        val machines = listOf("1".repeat(64), "2".repeat(64))
        val task = VerifyTask(UUID(7, 1), "a".repeat(64), byteArrayOf(1, 2), "f".repeat(64), deadline, 21_600, machines)
        val step =
            step(1).copy(
                action = Action.VERIFY,
                repositoryName = "shared",
                snapshotId = "4e5f",
                verifyTask = task,
            )

        val command = MutFlow.underTest { RunSteps.of(step) }

        val expected =
            ProtoVerifyTask
                .newBuilder()
                .setJobId(UUID(7, 1).toString())
                .setRepositoryId("a".repeat(64))
                .setFingerprint(ByteString.copyFrom(byteArrayOf(1, 2)))
                .setFingerprintSha256("f".repeat(64))
                .setDeadline(
                    com.google.protobuf.Timestamp
                        .newBuilder()
                        .setSeconds(deadline.epochSecond),
                ).addAllSourceMachineIds(machines)
                .build()
        assertEquals(expected, command.verify)
        assertEquals("4e5f", command.snapshotId)
        assertEquals("shared", command.repositoryName)
        assertEquals(21_600L, command.timeout.seconds)
    }

    @Test
    fun `a verify step without a fingerprint sends none, a backup step has no verify task and no timeout`() {
        val task = VerifyTask(UUID(7, 1), "a".repeat(64), null, null, Instant.EPOCH, 60)
        val verify = MutFlow.underTest { RunSteps.of(step(1).copy(action = Action.VERIFY, verifyTask = task)) }
        val sent = listOf(verify.verify.fingerprint, verify.verify.fingerprintSha256)
        assertEquals(listOf<Any>(ByteString.EMPTY, ""), sent)

        val backup = MutFlow.underTest { RunSteps.of(step(1)) }
        assertEquals(listOf(false, false), listOf(backup.hasVerify(), backup.hasTimeout()))
    }

    /**
     * ADR 0008 and V4 test 3: a verification's RunStep names the repository, the snapshot and the plugin; it
     * holds no field of a secret, no URL, no password file. Every field the message sets is listed here, so a new
     * one fails this test until someone looks at it.
     */
    @Test
    fun `a verify RunStep sets only fields that hold no secret`() {
        val machines = listOf("1")
        val task = VerifyTask(UUID(7, 1), "a".repeat(64), byteArrayOf(1), "f".repeat(64), Instant.EPOCH, 60, machines)
        val step = step(1).copy(action = Action.VERIFY, repositoryName = "shared", snapshotId = "s", verifyTask = task)

        val command = MutFlow.underTest { RunSteps.of(step) }

        val set =
            command.allFields.keys
                .map { it.name }
                .toSet()
        val expected =
            setOf(
                "command_id",
                "plugin",
                "config_json",
                "action",
                "repository_name",
                "snapshot_id",
                "timeout",
                "tags",
                "verify",
            )
        assertEquals(expected, set)
        assertEquals(
            setOf("job_id", "repository_id", "fingerprint", "fingerprint_sha256", "deadline", "source_machine_ids"),
            command.verify.allFields.keys
                .map { it.name }
                .toSet(),
        )
    }

    @Test
    fun `a script step has no source, its source tag is empty, which restic and the agent accept`() {
        val command = MutFlow.underTest { RunSteps.of(step(1).copy(action = Action.RUN, sourceId = null)) }
        assertEquals("", command.tagsMap["sard.source"])
        assertEquals(setOf("sard.step", "sard.run", "sard.source"), command.tagsMap.keys)
        assertTrue(command.tagsMap.all { (k, v) -> k.isNotEmpty() && ',' !in k + v }, "agent: pluginhost.resticTags")
    }

    @Test
    fun `every action maps to its proto value, a script step has no repository`() {
        val expected =
            mapOf(
                Action.BACKUP to ProtoAction.ACTION_BACKUP,
                Action.RESTORE to ProtoAction.ACTION_RESTORE,
                Action.VERIFY to ProtoAction.ACTION_VERIFY,
                Action.RUN to ProtoAction.ACTION_RUN,
            )
        for ((action, proto) in expected) {
            val command = MutFlow.underTest { RunSteps.of(step(1).copy(action = action, repositoryName = null)) }
            assertEquals(listOf<Any>(proto, ""), listOf(command.action, command.repositoryName))
        }
    }

    @Test
    fun `the lost window is two heartbeats by default, the check interval the stream's`() {
        val heartbeat = Duration.ofSeconds(30)
        val settings = MutFlow.underTest { DispatchProperties().settings(heartbeat, Duration.ofSeconds(15)) }
        assertEquals(DispatchSettings(Duration.ofSeconds(60), Duration.ofSeconds(15)), settings)

        val properties = DispatchProperties(3, Duration.ofSeconds(5))
        val own = MutFlow.underTest { properties.settings(heartbeat, Duration.ofSeconds(15)) }
        assertEquals(DispatchSettings(Duration.ofSeconds(90), Duration.ofSeconds(5)), own)
    }

    @Test
    fun `a window of less than one heartbeat and a non-positive interval are refused`() {
        val heartbeat = Duration.ofSeconds(30)
        val none = DispatchProperties(0)
        assertFailsWith<IllegalArgumentException> { MutFlow.underTest { none.settings(heartbeat, heartbeat) } }
        val one = MutFlow.underTest { DispatchProperties(1).settings(heartbeat, heartbeat) }
        assertEquals(heartbeat, one.lostAfter)
        assertFailsWith<IllegalArgumentException> {
            MutFlow.underTest { DispatchProperties(2, Duration.ZERO).settings(heartbeat, heartbeat) }
        }
    }
}
