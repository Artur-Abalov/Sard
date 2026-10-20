// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.fleet

import java.math.BigInteger
import java.time.Duration
import java.time.Instant

private val NANOS_PER_SECOND: BigInteger = BigInteger.valueOf(1_000_000_000L)
private val YEAR_DAYS: BigInteger = BigInteger.valueOf(365L)
private const val WARNING_DAYS = 30L
private val RENEWAL_PARTS: BigInteger = BigInteger.valueOf(3L)

/** Where a certificate stands in its lifetime (C1 phase 3, П33). */
enum class CertificateState {
    OK,
    RENEWAL_DUE,
    EXPIRING,
    EXPIRED,
}

/**
 * The lifetime of one certificate: from its `notBefore` to its `notAfter`. Every threshold is a share of this
 * lifetime, not of a setting (П33): after a setting changes or the CA clips a certificate (П3), the thresholds
 * follow the certificate. Comparisons are exact: shares are compared as products, never as rounded values.
 */
class AgentCertificateLifetime(
    val notBefore: Instant,
    val notAfter: Instant,
) {
    fun remaining(now: Instant): Duration = Duration.between(now, notAfter)

    /** Whole days to `notAfter`, rounded down, never negative (the same as `certificate_days_left` of the agent). */
    fun daysLeft(now: Instant): Long = remaining(now).toDays().coerceAtLeast(0)

    fun state(now: Instant): CertificateState =
        when {
            !isLeft(now) -> CertificateState.EXPIRED
            reached(WARNING_DAYS, now) -> CertificateState.EXPIRING
            remainingNanos(now) * RENEWAL_PARTS <= lifetimeNanos() -> CertificateState.RENEWAL_DUE
            else -> CertificateState.OK
        }

    /** Whether at most [days] of 365 of the lifetime is left, and some of it is (П36). */
    fun reached(
        days: Long,
        now: Instant,
    ): Boolean = isLeft(now) && remainingNanos(now) * YEAR_DAYS <= lifetimeNanos() * days.toBigInteger()

    private fun isLeft(now: Instant) = now.isBefore(notAfter)

    private fun remainingNanos(now: Instant) = nanos(remaining(now))

    private fun lifetimeNanos() = nanos(Duration.between(notBefore, notAfter))

    private fun nanos(duration: Duration): BigInteger {
        val whole = duration.seconds.toBigInteger() * NANOS_PER_SECOND
        return whole + duration.nano.toBigInteger()
    }
}
