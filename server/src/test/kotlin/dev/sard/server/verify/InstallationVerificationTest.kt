// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.verify

import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import java.time.Duration
import java.time.Instant
import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertEquals

private val NOW: Instant = Instant.parse("2026-10-26T12:00:00Z")

private fun standing(
    name: String,
    status: VerificationStatus?,
    passedAgo: Duration? = null,
    enabled: Boolean = true,
    stale: Boolean = false,
    level: Int? = if (passedAgo == null) null else 5,
) = SourceStanding(
    UUID.nameUUIDFromBytes(name.toByteArray()),
    name,
    VerificationSummary(
        enabled,
        status,
        passedAgo?.let { NOW - it },
        level,
        if (stale) NOW - Duration.ofDays(3) else null,
    ),
)

private fun hours(count: Long): Duration = Duration.ofHours(count)

/** V6: the installation's verification figures for the overview, as pure rules over the sources' standings. */
@MutFlowTest
class InstallationVerificationTest {
    private fun of(
        standings: List<SourceStanding>,
        anyVerifier: Boolean = true,
    ) = MutFlow.underTest { InstallationVerification.of(standings, NOW, anyVerifier) }

    private fun InstallationVerification.counts(): List<Int> {
        val everyone = listOf(enabled, verifiedLastDay, failed, notHeld)
        return everyone + listOf(stale, neverVerified, disabled)
    }

    @Test
    fun `the figures count the sources by the outcome of their last verification`() {
        val verification =
            of(
                listOf(
                    standing("a", VerificationStatus.PASSED, hours(3)),
                    standing("b", VerificationStatus.FAILED),
                    standing("c", VerificationStatus.CANNOT_VERIFY),
                    standing("d", VerificationStatus.INFRASTRUCTURE_FAILURE),
                    standing("e", null),
                    standing("f", null, enabled = false),
                ),
            )

        assertEquals(listOf(5, 1, 1, 2, 0, 1, 1), verification.counts())
    }

    @Test
    fun `no verifier at all is a source not held too`() {
        val verification = of(listOf(standing("a", VerificationStatus.NO_VERIFIER, hours(2))))

        assertEquals(1, verification.notHeld)
    }

    @Test
    fun `a pass is verified in the last day up to exactly 24 hours`() {
        fun lastDay(ago: Duration) = of(listOf(standing("a", VerificationStatus.PASSED, ago))).verifiedLastDay

        assertEquals(1, lastDay(hours(24) - Duration.ofMinutes(1)))
        assertEquals(1, lastDay(hours(24)))
        assertEquals(0, lastDay(hours(24) + Duration.ofSeconds(1)))
    }

    @Test
    fun `a failure after an old pass is both a failure and verified in the last day`() {
        val verification = of(listOf(standing("a", VerificationStatus.FAILED, hours(2))))

        assertEquals(listOf(1, 1), listOf(verification.failed, verification.verifiedLastDay))
    }

    @Test
    fun `open episodes are counted as stale`() {
        val verification =
            of(
                listOf(
                    standing("a", VerificationStatus.PASSED, hours(80), stale = true),
                    standing("b", null, stale = true),
                    standing("c", VerificationStatus.PASSED, hours(80)),
                ),
            )

        assertEquals(2, verification.stale)
    }

    @Test
    fun `a source with verification off is only counted as not verified on purpose`() {
        val verification =
            of(listOf(standing("f", VerificationStatus.FAILED, hours(720), enabled = false, stale = true)))

        assertEquals(listOf(0, 0, 0, 0, 0, 0, 1), verification.counts())
        assertEquals(emptyList(), verification.attention)
    }

    @Test
    fun `an installation without sources has zeros and nobody to attend to`() {
        val verification = of(emptyList(), anyVerifier = false)

        assertEquals(List(7) { 0 }, verification.counts())
        assertEquals(false, verification.noVerifier)
        assertEquals(emptyList(), verification.attention)
    }

    @Test
    fun `no verifier is a state of an installation that has sources to verify and no verifier`() {
        val sources = listOf(standing("a", null))

        assertEquals(true, of(sources, anyVerifier = false).noVerifier)
        assertEquals(false, of(sources, anyVerifier = true).noVerifier)
        assertEquals(false, of(listOf(standing("a", null, enabled = false)), anyVerifier = false).noVerifier)
    }

    @Test
    fun `attention lists failures first, then what was not held, what is stale, never verified, the oldest pass`() {
        val verification =
            of(
                listOf(
                    standing("fresh", VerificationStatus.PASSED, hours(1)),
                    standing("failed", VerificationStatus.FAILED, hours(40)),
                    standing("none", VerificationStatus.NO_VERIFIER),
                    standing("stale", VerificationStatus.PASSED, hours(60), stale = true),
                    standing("never", null),
                    standing("old", VerificationStatus.PASSED, hours(30)),
                ),
            )

        assertEquals(listOf("failed", "none", "stale", "never", "old", "fresh"), verification.attention.map { it.name })
    }

    @Test
    fun `sources of one kind go by the oldest pass, then by name`() {
        val verification =
            of(
                listOf(
                    standing("b", VerificationStatus.FAILED, hours(5)),
                    standing("a", VerificationStatus.FAILED, hours(5)),
                    standing("c", VerificationStatus.FAILED, hours(9)),
                    standing("d", VerificationStatus.FAILED),
                ),
            )

        assertEquals(listOf("d", "c", "a", "b"), verification.attention.map { it.name })
    }

    @Test
    fun `attention holds ten sources, the figures count them all`() {
        val verification = of(List(25) { standing("s%02d".format(it), VerificationStatus.FAILED) })

        assertEquals(10, verification.attention.size)
        assertEquals(25, verification.failed)
    }
}
