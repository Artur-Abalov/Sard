// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.enrollment

import dev.sard.server.pki.AgentCsr
import dev.sard.server.pki.InvalidCsrException
import java.time.Duration
import java.time.Instant

private const val NANOS_PER_SECOND = 1_000_000_000L

/** The checks of a renewal that need no database (C1, П4 and П12). */
internal object RenewalChecks {
    /**
     * The CSR is valid (the rules of Enroll) and its key is not [presentedKey], the public key of the certificate
     * presented (SubjectPublicKeyInfo, DER). Returns the key of the CSR.
     */
    fun requireNewKey(
        csrDer: ByteArray,
        presentedKey: ByteArray,
    ): ByteArray {
        val key = csrKey(csrDer)
        if (key.contentEquals(presentedKey)) {
            throw RenewalRejectedException(RenewalRejectedException.Reason.CSR_KEY_REUSED)
        }
        return key
    }

    /**
     * How long until another certificate may be issued, given the [latestIssue] of the agent: the rest of
     * [interval], rounded up to whole seconds (at least one); null when nothing holds the agent back.
     */
    fun retryAfter(
        latestIssue: Instant?,
        now: Instant,
        interval: Duration,
    ): Duration? {
        if (latestIssue == null) return null
        val wait = Duration.between(now, latestIssue + interval)
        if (!wait.isPositive) return null
        return Duration.ofSeconds((wait.toNanos() + NANOS_PER_SECOND - 1) / NANOS_PER_SECOND)
    }

    private fun csrKey(csrDer: ByteArray): ByteArray =
        try {
            AgentCsr.verifiedKey(csrDer).encoded
        } catch (e: InvalidCsrException) {
            throw RenewalRejectedException(RenewalRejectedException.Reason.CSR_INVALID, cause = e)
        }
}
