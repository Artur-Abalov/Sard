// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents

import dev.sard.proto.agent.v1.Action
import dev.sard.proto.agent.v1.RegisterRequest
import dev.sard.server.registration.AgentSnapshot
import dev.sard.server.registration.PluginAction
import dev.sard.server.registration.PluginEntry
import dev.sard.server.registration.RepositoryEntry

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
            pluginsList.map { PluginEntry(it.name, it.version, it.configSchema, it.actionsList.map(::pluginActionOf)) },
        repositories =
            repositoriesList.map { RepositoryEntry(it.name, it.backend, it.repositoryId, it.cryptoProvider) },
        secretNames = secretNamesList.toList(),
        scriptNames = scriptNamesList.toList(),
    )
