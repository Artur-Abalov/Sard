// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents

import dev.sard.proto.agent.v1.Action
import dev.sard.proto.agent.v1.Plugin
import dev.sard.proto.agent.v1.RegisterRequest
import dev.sard.proto.agent.v1.RepositoryInfo
import dev.sard.server.registration.AgentSnapshot
import dev.sard.server.registration.PluginAction
import dev.sard.server.registration.PluginEntry
import dev.sard.server.registration.RepositoryEntry
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import kotlin.test.Test
import kotlin.test.assertEquals

/** Register's message becomes the domain snapshot field for field; unknown actions become null. */
@MutFlowTest
class RegisterRequestsTest {
    @Test
    fun `every wire action maps to its domain action, unknown ones to null`() {
        val expected =
            mapOf(
                Action.ACTION_BACKUP to PluginAction.BACKUP,
                Action.ACTION_RESTORE to PluginAction.RESTORE,
                Action.ACTION_VERIFY to PluginAction.VERIFY,
                Action.ACTION_RUN to PluginAction.RUN,
                Action.ACTION_UNSPECIFIED to null,
                Action.UNRECOGNIZED to null,
            )
        assertEquals(expected, Action.entries.associateWith { MutFlow.underTest { pluginActionOf(it) } })
    }

    @Test
    fun `a request becomes the snapshot, the protocol version read as unsigned`() {
        val plugin =
            Plugin
                .newBuilder()
                .setName("pg")
                .setVersion("1")
                .setConfigSchema("{}")
                .addActions(Action.ACTION_RUN)
                .build()
        val repository =
            RepositoryInfo
                .newBuilder()
                .setName("main")
                .setBackend("s3")
                .setRepositoryId("id")
                .setCryptoProvider("gost")
                .build()
        val request =
            RegisterRequest
                .newBuilder()
                .setHostname("h")
                .setAgentVersion("v")
                .setProtocolVersion(-1)
                .setOs("linux")
                .setArch("arm64")
                .addPlugins(plugin)
                .addRepositories(repository)
                .addSecretNames("s")
                .addScriptNames("x")
                .build()
        val expected =
            AgentSnapshot(
                hostname = "h",
                agentVersion = "v",
                protocolVersion = 4_294_967_295L,
                os = "linux",
                arch = "arm64",
                plugins = listOf(PluginEntry("pg", "1", "{}", listOf(PluginAction.RUN))),
                repositories = listOf(RepositoryEntry("main", "s3", "id", "gost")),
                secretNames = listOf("s"),
                scriptNames = listOf("x"),
            )
        assertEquals(expected, MutFlow.underTest { request.toSnapshot() })
    }
}
