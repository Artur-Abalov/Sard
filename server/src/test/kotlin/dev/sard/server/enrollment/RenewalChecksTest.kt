// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.enrollment

import dev.sard.server.enrollment.RenewalRejectedException.Reason
import dev.sard.server.pki.AgentCsr
import dev.sard.server.pki.InvalidCsrException
import dev.sard.server.pki.PkiFixtures.resource
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import java.time.Duration
import java.time.Instant
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertNull
import kotlin.test.assertTrue

private val NOW = Instant.parse("2026-10-15T12:00:00Z")
private val HOUR = Duration.ofHours(1)

private fun keyOf(csr: String) = AgentCsr.verifiedKey(resource(csr)).encoded

/** C1, П4 and П12: the checks of a renewal that need no database. */
@MutFlowTest
class RenewalChecksTest {
    private fun refusal(
        csr: ByteArray,
        presented: ByteArray,
    ) = assertFailsWith<RenewalRejectedException> { MutFlow.underTest { RenewalChecks.requireNewKey(csr, presented) } }

    @Test
    fun `CSR на новом ключе годится, P-256 и P-384`() {
        MutFlow.underTest { RenewalChecks.requireNewKey(resource("agent-p256.csr"), keyOf("agent-p384.csr")) }
        MutFlow.underTest { RenewalChecks.requireNewKey(resource("agent-p384.csr"), keyOf("agent-p256.csr")) }
    }

    @Test
    fun `CSR на ключе предъявленного сертификата отклоняется как CSR_KEY_REUSED`() {
        val failure = refusal(resource("agent-p256.csr"), keyOf("agent-p256.csr"))
        assertEquals(Reason.CSR_KEY_REUSED, failure.reason)
        assertNull(failure.retryAfter)
    }

    @Test
    fun `Негодный CSR отклоняется как CSR_INVALID раньше сравнения ключей`() {
        val bad =
            listOf(
                ByteArray(0),
                ByteArray(64) { it.toByte() },
                resource("agent-bad-signature.csr"),
                resource("agent-rsa.csr"),
                resource("agent-ed25519.csr"),
                resource("agent-p521.csr"),
            )
        for (csr in bad) {
            val failure = refusal(csr, keyOf("agent-p256.csr"))
            assertEquals(Reason.CSR_INVALID, failure.reason)
            assertTrue(failure.cause is InvalidCsrException, "${failure.cause}")
        }
    }

    private fun retryAfter(
        latest: Instant?,
        now: Instant,
        interval: Duration = HOUR,
    ) = MutFlow.underTest { RenewalChecks.retryAfter(latest, now, interval) }

    @Test
    fun `Без прежних выпусков ждать нечего`() {
        assertNull(retryAfter(null, NOW))
    }

    @Test
    fun `Ровно по окончании интервала выпуск разрешён`() {
        assertNull(retryAfter(NOW, NOW + HOUR))
        assertNull(retryAfter(NOW, NOW + HOUR + Duration.ofSeconds(1)))
    }

    @Test
    fun `Задержка это время до конца интервала, округлённое вверх до целых секунд`() {
        assertEquals(Duration.ofSeconds(2400), retryAfter(NOW, NOW + Duration.ofMinutes(20)))
        assertEquals(Duration.ofSeconds(1), retryAfter(NOW, NOW + HOUR - Duration.ofNanos(1)))
        assertEquals(Duration.ofSeconds(1), retryAfter(NOW, NOW + HOUR - Duration.ofMillis(500)))
        assertEquals(Duration.ofSeconds(2), retryAfter(NOW, NOW + HOUR - Duration.ofMillis(1001)))
        assertEquals(Duration.ofSeconds(3600), retryAfter(NOW, NOW))
        assertEquals(Duration.ofSeconds(60), retryAfter(NOW, NOW + Duration.ofMinutes(4), Duration.ofMinutes(5)))
    }
}
