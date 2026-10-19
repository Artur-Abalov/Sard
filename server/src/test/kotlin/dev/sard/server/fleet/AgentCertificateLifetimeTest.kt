// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.fleet

import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import java.time.Duration
import java.time.Instant
import kotlin.test.Test
import kotlin.test.assertEquals

private val T0: Instant = Instant.parse("2026-10-15T12:00:00Z")

private fun lifetime(validity: String) = AgentCertificateLifetime(T0, T0 + Duration.parse(validity))

private fun Duration.after() = T0 + this

private fun state(
    validity: String,
    elapsed: Duration,
) = MutFlow.underTest { lifetime(validity).state(elapsed.after()) }

private fun daysLeft(
    validity: String,
    elapsed: Duration,
) = MutFlow.underTest { lifetime(validity).daysLeft(elapsed.after()) }

private val ONE_SECOND: Duration = Duration.ofSeconds(1)

/** C1 phase 3, П33: the state and the days left of a certificate follow from its own lifetime and the clock. */
@MutFlowTest
class AgentCertificateLifetimeTest {
    @Test
    fun `Состояние сертификата на 365 суток следует за долями срока`() {
        val twoThirds = Duration.ofDays(243).plusHours(8)
        val warning = Duration.ofDays(335)
        val year = Duration.ofDays(365)
        val cases =
            listOf(
                Triple(twoThirds - ONE_SECOND, CertificateState.OK, 121L),
                Triple(twoThirds, CertificateState.RENEWAL_DUE, 121L),
                Triple(warning - ONE_SECOND, CertificateState.RENEWAL_DUE, 30L),
                Triple(warning, CertificateState.EXPIRING, 30L),
                Triple(Duration.ofDays(364).plusHours(23), CertificateState.EXPIRING, 0L),
                Triple(year - ONE_SECOND, CertificateState.EXPIRING, 0L),
                Triple(year, CertificateState.EXPIRED, 0L),
                Triple(Duration.ofDays(400), CertificateState.EXPIRED, 0L),
            )
        for ((elapsed, expected, days) in cases) {
            assertEquals(expected, state("P365D", elapsed), "state at $elapsed")
            assertEquals(days, daysLeft("P365D", elapsed), "days at $elapsed")
        }
    }

    @Test
    fun `Состояние сертификата на 10 минут следует за долями срока`() {
        val cases =
            listOf(
                Triple(Duration.ofSeconds(399), CertificateState.OK, 0L),
                Triple(Duration.ofSeconds(400), CertificateState.RENEWAL_DUE, 0L),
                Triple(Duration.ofSeconds(550), CertificateState.RENEWAL_DUE, 0L),
                Triple(Duration.ofSeconds(551), CertificateState.EXPIRING, 0L),
                Triple(Duration.ofMinutes(10), CertificateState.EXPIRED, 0L),
            )
        for ((elapsed, expected, days) in cases) {
            assertEquals(expected, state("PT10M", elapsed), "state at $elapsed")
            assertEquals(days, daysLeft("PT10M", elapsed), "days at $elapsed")
        }
    }

    @Test
    fun `Сравнение точное и не теряет долей секунды`() {
        // 30/365 of PT10M is 49.315 seconds: 49.4 seconds left is not yet the warning, 49.3 is.
        val tenMinutes = Duration.ofMinutes(10)
        assertEquals(CertificateState.RENEWAL_DUE, state("PT10M", tenMinutes - Duration.ofMillis(49_400)))
        assertEquals(CertificateState.EXPIRING, state("PT10M", tenMinutes - Duration.ofMillis(49_300)))
    }

    @Test
    fun `Срок в сто лет не переполняет сравнение`() {
        val lifetime = AgentCertificateLifetime(T0, T0 + Duration.ofDays(36_500))
        assertEquals(CertificateState.OK, MutFlow.underTest { lifetime.state(T0) })
    }

    @Test
    fun `Порог достигнут, когда остаток по 365 не больше доли срока, и пока срок не вышел`() {
        val year = lifetime("P365D")
        val cases =
            listOf(
                Triple(Duration.ofDays(335) - ONE_SECOND, 30, false),
                Triple(Duration.ofDays(335), 30, true),
                Triple(Duration.ofDays(358) - ONE_SECOND, 7, false),
                Triple(Duration.ofDays(358), 7, true),
                Triple(Duration.ofDays(364), 1, true),
                Triple(Duration.ofDays(365) - ONE_SECOND, 1, true),
                Triple(Duration.ofDays(365), 1, false),
                Triple(Duration.ofDays(365), 30, false),
            )
        for ((elapsed, days, expected) in cases) {
            val reached = MutFlow.underTest { year.reached(days.toLong(), elapsed.after()) }
            assertEquals(expected, reached, "$days at $elapsed")
        }
    }

    @Test
    fun `Остаток отсчитывается до конца срока`() {
        assertEquals(Duration.ofHours(5), lifetime("PT10H").remaining(T0 + Duration.ofHours(5)))
    }
}
