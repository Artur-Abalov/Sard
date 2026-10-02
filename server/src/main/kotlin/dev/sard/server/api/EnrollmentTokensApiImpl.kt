// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import dev.sard.server.enrollment.DEFAULT_TTL
import dev.sard.server.enrollment.EnrollmentTokenState
import dev.sard.server.enrollment.EnrollmentTokenSummary
import dev.sard.server.enrollment.EnrollmentTokens
import dev.sard.server.enrollment.RevokeRejection
import dev.sard.server.enrollment.RevokeResult
import dev.sard.server.extension.TenantResolver
import dev.sard.server.persistence.PageKey
import org.springframework.stereotype.Component
import java.time.Clock
import java.time.Duration
import java.util.UUID

/** The token endpoints over [EnrollmentTokens], in the tenant of the session. */
@Component
class EnrollmentTokensApiImpl(
    private val tokens: EnrollmentTokens,
    private val tenants: TenantResolver,
    private val clock: Clock,
) : EnrollmentTokensApi {
    override fun createEnrollmentToken(request: CreateEnrollmentTokenRequest): CreatedEnrollmentToken {
        val ttl = request.ttlSeconds?.let(Duration::ofSeconds) ?: DEFAULT_TTL
        val issued = tokens.create(tenants.currentTenantId(), ttl, request.label.orEmpty())
        return CreatedEnrollmentToken(
            issued.id,
            issued.reveal(),
            issued.command(),
            issued.expiresAt,
            issued.endpointConfigured(),
        )
    }

    override fun listEnrollmentTokens(
        status: EnrollmentTokenStatus?,
        cursor: String?,
        limit: Int,
    ): EnrollmentTokenPage {
        val page = pageRequest(CursorKind.TOKENS, cursor, limit)
        val rows = tokens.list(tenants.currentTenantId(), status?.let(::stateOf), page.after, page.fetch)
        val slice = page.slice(rows) { PageKey(it.createdAt, it.id) }
        return EnrollmentTokenPage(slice.items.map(::cardOf), slice.nextCursor)
    }

    override fun getEnrollmentToken(tokenId: UUID): EnrollmentToken = card(tenants.currentTenantId(), tokenId)

    private fun card(
        tenant: UUID,
        tokenId: UUID,
    ): EnrollmentToken = cardOf(tokens.get(tenant, tokenId) ?: throw ResourceNotFound())

    override fun revokeEnrollmentToken(tokenId: UUID): EnrollmentToken {
        val tenant = tenants.currentTenantId()
        return when (val result = tokens.revoke(tenant, tokenId, clock.instant())) {
            is RevokeResult.Revoked -> card(tenant, tokenId)
            is RevokeResult.Rejected -> throw conflict(result.reason, tokens.get(tenant, tokenId))
            RevokeResult.NotFound -> throw ResourceNotFound()
        }
    }

    private fun conflict(
        reason: RevokeRejection,
        card: EnrollmentTokenSummary?,
    ): TokenConflict =
        when (reason) {
            RevokeRejection.USED -> TokenConflict(ErrorCode.TOKEN_USED, card?.agentId)
            RevokeRejection.EXPIRED -> TokenConflict(ErrorCode.TOKEN_EXPIRED, null)
        }

    private fun stateOf(status: EnrollmentTokenStatus): EnrollmentTokenState =
        when (status) {
            EnrollmentTokenStatus.ACTIVE -> EnrollmentTokenState.ACTIVE
            EnrollmentTokenStatus.USED -> EnrollmentTokenState.USED
            EnrollmentTokenStatus.EXPIRED -> EnrollmentTokenState.EXPIRED
            EnrollmentTokenStatus.REVOKED -> EnrollmentTokenState.REVOKED
        }

    private fun statusOf(state: EnrollmentTokenState): EnrollmentTokenStatus =
        when (state) {
            EnrollmentTokenState.ACTIVE -> EnrollmentTokenStatus.ACTIVE
            EnrollmentTokenState.USED -> EnrollmentTokenStatus.USED
            EnrollmentTokenState.EXPIRED -> EnrollmentTokenStatus.EXPIRED
            EnrollmentTokenState.REVOKED -> EnrollmentTokenStatus.REVOKED
        }

    private fun cardOf(token: EnrollmentTokenSummary) =
        EnrollmentToken(
            token.id,
            statusOf(token.state),
            token.createdAt,
            token.expiresAt,
            token.usedAt,
            token.revokedAt,
            token.agentId,
            token.label.ifEmpty { null },
        )
}
