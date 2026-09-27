// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.enrollment

import java.time.Instant

/** A token's state, computed at read time (decision 9): priority used > revoked > expired > active. */
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
