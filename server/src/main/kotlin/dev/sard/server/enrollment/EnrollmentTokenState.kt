// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.enrollment

import java.time.Instant

/**
 * A token's state, computed at read time (decision 9): priority used > revoked > expired > active.
 * The database (enrollment_tokens_revocation_check) guarantees a row is never both used and
 * revoked, so in practice at most one of the first two branches below can ever apply; the order
 * is kept because it is the order the rejection contract and this enum's own tests document.
 */
enum class EnrollmentTokenState {
    ACTIVE,
    USED,
    EXPIRED,
    REVOKED,
    ;

    companion object {
        fun of(
            usedAt: Instant?,
            revokedAt: Instant?,
            expiresAt: Instant,
            now: Instant,
        ): EnrollmentTokenState =
            when {
                usedAt != null -> USED
                revokedAt != null -> REVOKED
                !now.isBefore(expiresAt) -> EXPIRED
                else -> ACTIVE
            }
    }
}
