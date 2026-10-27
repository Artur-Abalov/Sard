// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import dev.sard.server.extension.TenantResolver
import dev.sard.server.fleet.Overviews
import dev.sard.server.verify.InstallationVerification
import dev.sard.server.verify.SourceStanding
import dev.sard.server.verify.Verifications
import org.springframework.stereotype.Component

/** The overview endpoint over [Overviews], in the tenant of the session. */
@Component
class OverviewApiImpl(
    private val overviews: Overviews,
    private val verifications: Verifications,
    private val tenants: TenantResolver,
) : OverviewApi {
    override fun overview(): Overview {
        val tenant = tenants.currentTenantId()
        val view = overviews.of(tenant)
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
            verificationOf(verifications.overview(tenant)),
        )
    }
}

private fun verificationOf(verification: InstallationVerification) =
    VerificationOverview(
        enabled = verification.enabled,
        verifiedLastDay = verification.verifiedLastDay,
        failed = verification.failed,
        notHeld = verification.notHeld,
        stale = verification.stale,
        neverVerified = verification.neverVerified,
        disabled = verification.disabled,
        noVerifier = verification.noVerifier,
        attention = verification.attention.map(::attentionOf),
    )

private fun attentionOf(standing: SourceStanding) =
    AttentionSource(
        sourceId = standing.sourceId,
        sourceName = standing.name,
        status = standing.summary.status?.let(::outcomeOf),
        lastVerifiedAt = standing.summary.lastVerifiedAt,
        lastVerifiedLevel = standing.summary.lastVerifiedLevel,
        staleSince = standing.summary.staleSince,
    )
