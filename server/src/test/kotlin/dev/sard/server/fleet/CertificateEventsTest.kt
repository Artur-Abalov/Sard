// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.fleet

import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import java.time.Duration
import java.time.Instant
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNull

private val T0: Instant = Instant.parse("2026-10-15T12:00:00Z")

private fun due(
    validity: String,
    elapsed: Duration,
    recorded: Set<CertificateEvent> = emptySet(),
): CertificateEvent? =
    MutFlow.underTest {
        CertificateEvent.due(AgentCertificateLifetime(T0, T0 + Duration.parse(validity)), T0 + elapsed, recorded)
    }

/** C1 phase 3, П36: which event a check records for a certificate, if any. */
@MutFlowTest(includeTargets = [CertificateEvent::class])
class CertificateEventsTest {
    @Test
    fun `Пока порог не достигнут, события нет`() {
        assertNull(due("P365D", Duration.ofDays(335).minusSeconds(1)))
    }

    @Test
    fun `Достигнутый порог 30 записывается`() {
        assertEquals(CertificateEvent.THRESHOLD_30, due("P365D", Duration.ofDays(335)))
    }

    @Test
    fun `Записанный порог второй раз не записывается`() {
        assertNull(due("P365D", Duration.ofDays(340), setOf(CertificateEvent.THRESHOLD_30)))
    }

    @Test
    fun `Если достигнуто несколько порогов, записывается наименьший`() {
        assertEquals(CertificateEvent.THRESHOLD_7, due("P365D", Duration.ofDays(360)))
        assertEquals(CertificateEvent.THRESHOLD_1, due("P365D", Duration.ofDays(364).plusHours(23)))
    }

    @Test
    fun `Пройденный при простое порог не записывается позже`() {
        val recorded = setOf(CertificateEvent.THRESHOLD_7)
        assertNull(due("P365D", Duration.ofDays(361), recorded))
        assertEquals(CertificateEvent.THRESHOLD_1, due("P365D", Duration.ofDays(364), recorded))
    }

    @Test
    fun `Порог меньше записанного записывается`() {
        assertEquals(
            CertificateEvent.THRESHOLD_7,
            due("P365D", Duration.ofDays(358), setOf(CertificateEvent.THRESHOLD_30)),
        )
    }

    @Test
    fun `Пороги масштабируются по сроку сертификата`() {
        val t30 = CertificateEvent.THRESHOLD_30
        val t7 = CertificateEvent.THRESHOLD_7
        val t1 = CertificateEvent.THRESHOLD_1
        val expected =
            mapOf(
                550L to emptySet(),
                551L to setOf(t30),
                588L to setOf(t30),
                589L to setOf(t30, t7),
                598L to setOf(t30, t7),
                599L to setOf(t30, t7, t1),
            )
        for ((last, events) in expected) {
            val recorded = mutableSetOf<CertificateEvent>()
            for (second in 540L..last) due("PT10M", Duration.ofSeconds(second), recorded)?.let(recorded::add)
            assertEquals(events, recorded, "checks every second until $last")
        }
    }

    @Test
    fun `Истёкший сертификат даёт только событие истечения`() {
        assertEquals(CertificateEvent.EXPIRED, due("P365D", Duration.ofDays(370)))
        assertEquals(CertificateEvent.EXPIRED, due("P365D", Duration.ofDays(365)))
    }

    @Test
    fun `Событие истечения записывается и после порогов, но один раз`() {
        val thresholds =
            setOf(CertificateEvent.THRESHOLD_30, CertificateEvent.THRESHOLD_7, CertificateEvent.THRESHOLD_1)
        assertEquals(CertificateEvent.EXPIRED, due("P365D", Duration.ofDays(365), thresholds))
        assertNull(due("P365D", Duration.ofDays(366), thresholds + CertificateEvent.EXPIRED))
    }

    @Test
    fun `Событие читается и пишется своим именем в базе`() {
        for (event in CertificateEvent.entries) assertEquals(event, CertificateEvent.of(event.wire))
        val wire = listOf("threshold-30", "threshold-7", "threshold-1", "expired")
        assertEquals(wire, CertificateEvent.entries.map { it.wire })
    }
}
