// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents

import dev.sard.proto.agent.v1.Action
import dev.sard.proto.agent.v1.RegisterRequest
import dev.sard.proto.agent.v1.Sandbox
import dev.sard.proto.agent.v1.VerifierStatus
import dev.sard.server.registration.AgentSnapshot
import dev.sard.server.registration.PluginAction
import dev.sard.server.registration.PluginEntry
import dev.sard.server.registration.RepositoryEntry
import dev.sard.server.registration.SandboxKind
import dev.sard.server.registration.VerifierReport

/**
 * The wire action as the domain knows it; null for one it does not (unspecified, or a value
 * newer than this server), which the snapshot rules refuse. Exhaustive, no `else`.
 */
internal fun pluginActionOf(action: Action): PluginAction? =
    when (action) {
        Action.ACTION_BACKUP -> PluginAction.BACKUP
        Action.ACTION_RESTORE -> PluginAction.RESTORE
        Action.ACTION_VERIFY -> PluginAction.VERIFY
        Action.ACTION_RUN -> PluginAction.RUN
        Action.ACTION_UNSPECIFIED, Action.UNRECOGNIZED -> null
    }

/** Register's message as a domain snapshot; nothing is checked here (SnapshotRules does). */
internal fun RegisterRequest.toSnapshot(): AgentSnapshot =
    AgentSnapshot(
        hostname = hostname,
        agentVersion = agentVersion,
        protocolVersion = Integer.toUnsignedLong(protocolVersion),
        os = os,
        arch = arch,
        plugins =
            pluginsList.map {
                val actions = it.actionsList.map(::pluginActionOf)
                PluginEntry(it.name, it.version, it.configSchema, actions, it.restoreConfigSchema)
            },
        repositories =
            repositoriesList.map { RepositoryEntry(it.name, it.backend, it.repositoryId, it.cryptoProvider) },
        secretNames = secretNamesList.toList(),
        scriptNames = scriptNamesList.toList(),
        machineId = machineId,
        restoreDir = restoreDir,
        verifier = if (hasVerifier()) verifier.toReport() else null,
    )

/** Free space above the signed range reads negative on the JVM: unknown, as an unset one. */
private fun VerifierStatus.toReport() =
    VerifierReport(
        workFreeBytes = if (hasWorkFreeBytes() && workFreeBytes >= 0) workFreeBytes else null,
        sandbox = sandboxOf(sandbox),
    )

private fun sandboxOf(sandbox: Sandbox): SandboxKind? =
    when (sandbox) {
        Sandbox.SANDBOX_NONE -> SandboxKind.NONE
        Sandbox.SANDBOX_DOCKER -> SandboxKind.DOCKER
        Sandbox.SANDBOX_PODMAN -> SandboxKind.PODMAN
        Sandbox.SANDBOX_UNSPECIFIED, Sandbox.UNRECOGNIZED -> null
    }
