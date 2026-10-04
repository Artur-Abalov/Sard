// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents.dispatch

import dev.sard.proto.agent.v1.RunStep
import dev.sard.server.runs.Action
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import java.time.Duration
import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertTrue
import dev.sard.proto.agent.v1.Action as ProtoAction

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
