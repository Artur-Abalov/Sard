// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import dev.sard.server.extension.TenantResolver
import dev.sard.server.fleet.Overviews
import org.springframework.stereotype.Component

/** The overview endpoint over [Overviews], in the tenant of the session. */
@Component
class OverviewApiImpl(
    private val overviews: Overviews,
    private val tenants: TenantResolver,
) : OverviewApi {
    override fun overview(): Overview {
        val view = overviews.of(tenants.currentTenantId())
        val steps = view.firstSteps
        return Overview(
            view.agentsOnline,
            view.agentsTotal,
            FirstSteps(
                steps.tokenIssued,
                steps.agentConnected,
                steps.repositoryInitialized,
                steps.sourceCreated,
                steps.backupSucceeded,
                steps.complete,
            ),
        )
    }
}
