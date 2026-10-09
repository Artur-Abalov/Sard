// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.enrollment

import dev.sard.server.pki.CaFingerprint
import dev.sard.server.pki.CaReplacementListener
import org.slf4j.LoggerFactory
import org.springframework.jdbc.core.JdbcTemplate
import java.sql.Timestamp
import java.time.Clock

private val log = LoggerFactory.getLogger(TokensRevokedOnCaReplacement::class.java)

/**
 * A token carries the fingerprint of the CA it was issued under, so after a replacement of the CA (F4a, Р11,
 * OQ-191) the active ordinary tokens of every tenant are of no use: an agent would refuse them. They are revoked
 * and the count is logged once. Built-in tokens do not exist before the step ca, which a replacement precedes.
 */
class TokensRevokedOnCaReplacement(
    private val jdbc: JdbcTemplate,
    private val clock: Clock,
) : CaReplacementListener {
    override fun replaced(
        previous: CaFingerprint,
        current: CaFingerprint,
    ) {
        val now = Timestamp.from(clock.instant())
        val revoked =
            jdbc.update(
                "update enrollment_tokens set revoked_at = ? where builtin = false and used_at is null " +
                    "and revoked_at is null and expires_at > ?",
                now,
                now,
            )
        if (revoked > 0) log.warn("CA replaced ({}): {} active enrollment tokens revoked", previous.hex, revoked)
    }
}
