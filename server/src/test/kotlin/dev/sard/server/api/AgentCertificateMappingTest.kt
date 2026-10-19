// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import dev.sard.server.fleet.AgentCertificateLifetime
import dev.sard.server.fleet.CertificateState
import dev.sard.server.fleet.CurrentCertificate
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import java.time.Duration
import java.time.Instant
import kotlin.test.Test
import kotlin.test.assertEquals

private val ISSUED: Instant = Instant.parse("2026-10-15T12:00:00Z")

private fun shown(
    validity: Duration,
    elapsed: Duration,
    renewalIssuedAt: Instant? = null,
) = MutFlow.underTest {
    val lifetime = AgentCertificateLifetime(ISSUED, ISSUED + validity)
    CurrentCertificate("1", lifetime, renewalIssuedAt).toApi(ISSUED + elapsed)
}

/** C1 phase 3, П33: the certificate as the REST API shows it. */
@MutFlowTest
class AgentCertificateMappingTest {
    @Test
    fun `Сертификат показан со сроком, остатком суток и состоянием по часам сервера`() {
        val renewal = ISSUED.plusSeconds(5)
        val api = shown(Duration.ofDays(365), Duration.ofDays(100), renewal)

        assertEquals(ISSUED, api.notBefore)
        assertEquals(ISSUED.plus(Duration.ofDays(365)), api.notAfter)
        assertEquals(265, api.daysLeft)
        assertEquals(AgentCertificateState.OK, api.state)
        assertEquals(renewal, api.renewalIssuedAt)
    }

    @Test
    fun `Каждое состояние сертификата показано своим`() {
        val year = Duration.ofDays(365)
        assertEquals(AgentCertificateState.RENEWAL_DUE, shown(year, Duration.ofDays(250)).state)
        assertEquals(AgentCertificateState.EXPIRING, shown(year, Duration.ofDays(340)).state)
        assertEquals(AgentCertificateState.EXPIRED, shown(year, Duration.ofDays(366)).state)
    }

    @Test
    fun `Границы состояний включают точное значение`() {
        val year = Duration.ofDays(365)
        assertEquals(AgentCertificateState.RENEWAL_DUE, shown(Duration.ofDays(300), Duration.ofDays(200)).state)
        assertEquals(AgentCertificateState.OK, shown(Duration.ofDays(300), Duration.ofDays(200).minusNanos(1)).state)
        assertEquals(AgentCertificateState.EXPIRING, shown(year, Duration.ofDays(335)).state)
        assertEquals(AgentCertificateState.RENEWAL_DUE, shown(year, Duration.ofDays(335).minusNanos(1)).state)
        assertEquals(AgentCertificateState.EXPIRING, shown(year, year.minusNanos(1)).state)
        assertEquals(AgentCertificateState.EXPIRED, shown(year, year).state)
        assertEquals(0, shown(year, year).daysLeft)
    }

    @Test
    fun `Остаток суток у истёкшего равен нулю, без продления нет времени выдачи`() {
        val api = shown(Duration.ofDays(365), Duration.ofDays(400))
        assertEquals(0, api.daysLeft)
        assertEquals(null, api.renewalIssuedAt)
    }

    @Test
    fun `Остаток суток не выходит за целое`() {
        val api = shown(Duration.ofDays(Int.MAX_VALUE.toLong() + 10), Duration.ZERO)
        assertEquals(Int.MAX_VALUE, api.daysLeft)
    }
}
