// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import dev.sard.server.extension.TenantResolver
import dev.sard.server.fleet.Repositories
import dev.sard.server.fleet.RepositoryHolder
import dev.sard.server.fleet.RepositorySummary
import org.springframework.stereotype.Component

/** The repositories endpoint over [Repositories], in the tenant of the session. */
@Component
class RepositoriesApiImpl(
    private val repositories: Repositories,
    private val tenants: TenantResolver,
) : RepositoriesApi {
    override fun listRepositories(): RepositoryList {
        val overview = repositories.overview(tenants.currentTenantId())
        return RepositoryList(overview.items.map(::infoOf), overview.withoutRecoveryKey)
    }

    private fun infoOf(summary: RepositorySummary) =
        RepositoryInfo(
            summary.repositoryId,
            summary.recoveryKey.toApi(),
            summary.reportedAt,
            summary.reportedByAgentId,
            summary.recoveryKeyCommand,
            summary.holders.map(::holderOf),
        )

    private fun holderOf(holder: RepositoryHolder) =
        RepositoryHolderInfo(holder.agentId, holder.hostname, holder.role.toApi(), holder.repositoryName)
}
