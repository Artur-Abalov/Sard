// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.fleet

import java.time.Instant

private const val MONTH = 30L
private const val WEEK = 7L
private const val DAY = 1L

/**
 * A fact about a certificate that the periodic check records once and a notification tells of (C1 phase 3, П36):
 * the certificate reached 30, 7 or 1 of 365 of its lifetime left, or it expired. [wire] is the stored name.
 */
enum class CertificateEvent(
    val wire: String,
    /** The share of 365 for a threshold; null for the expiry. */
    val days: Long?,
) {
    THRESHOLD_30("threshold-30", MONTH),
    THRESHOLD_7("threshold-7", WEEK),
    THRESHOLD_1("threshold-1", DAY),
    EXPIRED("expired", null),
    ;

    companion object {
        fun of(wire: String): CertificateEvent = entries.first { it.wire == wire }

        /**
         * The event to record for a certificate with [lifetime] at [now], given the events [recorded] already.
         * When several thresholds are reached and not recorded (downtime, a first start after an upgrade, a rare
         * check), only the smallest is recorded: the larger ones count as passed. An expired certificate gets
         * the expiry only, never a threshold.
         */
        fun due(
            lifetime: AgentCertificateLifetime,
            now: Instant,
            recorded: Set<CertificateEvent>,
        ): CertificateEvent? {
            val event = current(lifetime, now)
            return event?.takeUnless { it in recorded }
        }

        private fun current(
            lifetime: AgentCertificateLifetime,
            now: Instant,
        ): CertificateEvent? =
            if (lifetime.state(now) == CertificateState.EXPIRED) {
                EXPIRED
            } else {
                entries.lastOrNull { it.days != null && lifetime.reached(it.days, now) }
            }
    }
}
