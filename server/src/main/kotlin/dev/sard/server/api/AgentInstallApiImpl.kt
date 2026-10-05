// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import dev.sard.server.extension.TenantResolver
import dev.sard.server.fleet.Agents
import dev.sard.server.install.AgentInstalls
import org.springframework.stereotype.Component
import java.util.UUID
import dev.sard.server.install.FetchTool as DomainFetch
import dev.sard.server.install.InstallArch as DomainArch
import dev.sard.server.install.InstallFormat as DomainFormat
import dev.sard.server.install.InstallStep as DomainStep
import dev.sard.server.install.ReleaseKey as DomainKey

private const val MANUAL_INSTALL_DOC = "https://github.com/Artur-Abalov/sard/blob/main/docs/operations/agent-install.md"

/** The install endpoints over [AgentInstalls]; an upgrade is of an agent of the tenant of the session. */
@Component
class AgentInstallApiImpl(
    private val installs: AgentInstalls,
    private val key: DomainKey,
    private val agents: Agents,
    private val tenants: TenantResolver,
) : AgentInstallApi {
    override fun agentInstall(
        arch: InstallArch,
        format: InstallFormat,
        fetch: FetchTool,
    ): AgentInstall {
        val info = installs.install(arch.toDomain(), format.toDomain(), fetch.toDomain())
        return AgentInstall(
            info.downloadsEnabled,
            info.agentVersion,
            info.resticVersion,
            arch,
            format,
            info.signed,
            key.toWire(),
            MANUAL_INSTALL_DOC,
            info.steps.map { it.toWire() },
        )
    }

    override fun agentUpgrade(
        agentId: UUID,
        format: InstallFormat,
        fetch: FetchTool,
    ): AgentUpgrade {
        val agent = agents.get(tenants.currentTenantId(), agentId) ?: throw ResourceNotFound()
        val arch = agent.row.arch
        val info = installs.upgrade(arch, format.toDomain(), fetch.toDomain())
        return AgentUpgrade(
            info.downloadsEnabled,
            info.agentVersion,
            info.resticVersion,
            arch,
            format,
            info.signed,
            key.toWire(),
            MANUAL_INSTALL_DOC,
            info.steps.map { it.toWire() },
            info.reason?.twin<UpgradeReason>(),
        )
    }
}

private inline fun <reified T : Enum<T>> Enum<*>.twin(): T = enumValueOf<T>(name)

private fun InstallArch.toDomain(): DomainArch = twin()

private fun InstallFormat.toDomain(): DomainFormat = twin()

private fun FetchTool.toDomain(): DomainFetch = twin()

private fun DomainStep.toWire() = InstallStep(kind.twin(), commands, optional)

private fun DomainKey.toWire() = ReleaseKey(id, publicKey)
