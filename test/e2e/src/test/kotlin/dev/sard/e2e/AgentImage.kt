// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.e2e

import org.testcontainers.containers.GenericContainer
import org.testcontainers.containers.output.OutputFrame
import org.testcontainers.containers.startupcheck.OneShotStartupCheckStrategy
import org.testcontainers.utility.DockerImageName
import java.time.Duration

/** The sard-agent image (deploy/agent/Dockerfile), laid out as the packages install it (ADR 0018). */
internal object AgentImage {
    const val AGENT_BINARY = "/usr/libexec/sard/sard-agent"
    const val RESTIC_BINARY = "/usr/libexec/sard/restic"

    /** Runs [command] in a fresh container of the image to completion and returns its stdout. */
    fun run(vararg command: String): String {
        GenericContainer<Nothing>(DockerImageName.parse(E2e.agentImage)).use { container ->
            container.withCreateContainerCmdModifier { it.withEntrypoint(*command) }
            container.withStartupCheckStrategy(OneShotStartupCheckStrategy().withTimeout(Duration.ofSeconds(30)))
            container.start()
            return container.getLogs(OutputFrame.OutputType.STDOUT)
        }
    }
}
