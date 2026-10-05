// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.e2e

import org.testcontainers.containers.GenericContainer

/**
 * The sard-agent service of an enrolled host on the environment's network, waiting until the
 * agent dials the server. The tls files are the ones `sard-agent enroll` wrote on the host
 * ([AgentEnroller], [AgentHost]); the config is the host's.
 */
internal object AgentContainer {
    /** The name the agent container is tracked under in the environment's logs. */
    const val ALIAS = "sard-agent"

    /** [ownedFiles] (path to content) are written 0600 and owned by the agent, as the agent requires of secret files (A1). */
    fun of(
        agent: EnrolledAgent,
        ownedFiles: Map<String, String> = emptyMap(),
    ): GenericContainer<*> = agent.host.agent(ownedFiles)
}
