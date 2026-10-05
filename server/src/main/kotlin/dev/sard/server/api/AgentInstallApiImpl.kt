// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import dev.sard.server.extension.TenantResolver
import dev.sard.server.fleet.Agents
import dev.sard.server.install.AgentInstalls
import dev.sard.server.install.FetchTool
import dev.sard.server.install.InstallArch
import dev.sard.server.install.InstallFormat
import dev.sard.server.install.ReleaseKey
import org.springframework.stereotype.Component
import java.util.UUID

private const val MANUAL_INSTALL_DOC = "https://github.com/Artur-Abalov/sard/blob/main/docs/operations/agent-install.md"

/** The install endpoints over [AgentInstalls]; an upgrade is of an agent of the tenant of the session. */
@Component
class AgentInstallApiImpl(
    private val installs: AgentInstalls,
    private val key: ReleaseKey,
    private val agents: Agents,
    private val tenants: TenantResolver,
) : AgentInstallApi {
    override fun agentInstall(
        arch: InstallArch,
        format: InstallFormat,
        fetch: FetchTool,
    ): AgentInstall {
        val info = installs.install(arch, format, fetch)
        return AgentInstall(
            info.downloadsEnabled,
            info.agentVersion,
            info.resticVersion,
            arch,
            format,
            info.signed,
            key,
            MANUAL_INSTALL_DOC,
            info.steps,
        )
    }

    override fun agentUpgrade(
        agentId: UUID,
        format: InstallFormat,
        fetch: FetchTool,
    ): AgentUpgrade {
        val agent = agents.get(tenants.currentTenantId(), agentId) ?: throw ResourceNotFound()
        val arch = agent.row.arch
        val info = installs.upgrade(arch, format, fetch)
        return AgentUpgrade(
            info.downloadsEnabled,
            info.agentVersion,
            info.resticVersion,
            arch,
            format,
            info.signed,
            key,
            MANUAL_INSTALL_DOC,
            info.steps,
            info.reason,
        )
    }
}
