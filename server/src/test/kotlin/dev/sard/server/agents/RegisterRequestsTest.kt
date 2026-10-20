// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents

import dev.sard.proto.agent.v1.Action
import dev.sard.proto.agent.v1.Plugin
import dev.sard.proto.agent.v1.RegisterRequest
import dev.sard.proto.agent.v1.RepositoryInfo
import dev.sard.proto.agent.v1.Sandbox
import dev.sard.proto.agent.v1.VerifierStatus
import dev.sard.server.registration.AgentSnapshot
import dev.sard.server.registration.PluginAction
import dev.sard.server.registration.PluginEntry
import dev.sard.server.registration.RepositoryEntry
import dev.sard.server.registration.SandboxKind
import dev.sard.server.registration.VerifierReport
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse

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
                .setRestoreConfigSchema("""{"type":"object"}""")
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
                .setMachineId("m".repeat(64))
                .setRestoreDir("/srv/restore")
                .build()
        val expected =
            AgentSnapshot(
                hostname = "h",
                agentVersion = "v",
                protocolVersion = 4_294_967_295L,
                os = "linux",
                arch = "arm64",
                plugins = listOf(PluginEntry("pg", "1", "{}", listOf(PluginAction.RUN), """{"type":"object"}""")),
                repositories = listOf(RepositoryEntry("main", "s3", "id", "gost")),
                secretNames = listOf("s"),
                scriptNames = listOf("x"),
                machineId = "m".repeat(64),
                restoreDir = "/srv/restore",
            )
        assertEquals(expected, MutFlow.underTest { request.toSnapshot() })
    }

    @Test
    fun `a snapshot printed for a log does not show the machine id`() {
        val request =
            RegisterRequest
                .newBuilder()
                .setHostname("h")
                .setMachineId("c".repeat(64))
                .build()
        assertFalse("c".repeat(64) in request.toSnapshot().toString())
    }

    @Test
    fun `a request without a verifier status is an agent older than V4`() {
        val request = RegisterRequest.newBuilder().setHostname("h").build()

        assertEquals(null, MutFlow.underTest { request.toSnapshot() }.verifier)
    }

    @Test
    fun `a verifier status maps its free space and sandbox, unknown ones to null`() {
        val cases =
            mapOf(
                VerifierStatus.newBuilder().setWorkFreeBytes(7).setSandbox(Sandbox.SANDBOX_DOCKER) to
                    VerifierReport(7, SandboxKind.DOCKER),
                VerifierStatus.newBuilder().setSandbox(Sandbox.SANDBOX_PODMAN) to
                    VerifierReport(null, SandboxKind.PODMAN),
                VerifierStatus.newBuilder().setSandbox(Sandbox.SANDBOX_NONE) to VerifierReport(null, SandboxKind.NONE),
                VerifierStatus.newBuilder().setWorkFreeBytes(0) to VerifierReport(0, null),
                VerifierStatus.newBuilder().setWorkFreeBytes(-1) to VerifierReport(null, null),
                VerifierStatus.newBuilder().setSandboxValue(99) to VerifierReport(null, null),
                VerifierStatus.newBuilder() to VerifierReport(null, null),
            )
        for ((status, expected) in cases) {
            val request = RegisterRequest.newBuilder().setVerifier(status).build()
            assertEquals(expected, MutFlow.underTest { request.toSnapshot() }.verifier, status.toString())
        }
    }
}
