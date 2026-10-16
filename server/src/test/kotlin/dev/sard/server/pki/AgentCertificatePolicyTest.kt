// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.pki

import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import java.time.Duration
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertTrue

private const val VALIDITY_SETTING = "sard.pki.agent-certificate-validity"
private const val INTERVAL_SETTING = "sard.pki.agent-renewal-min-interval"

private fun policy(
    validity: String = "P365D",
    interval: String = "PT1H",
) = MutFlow.underTest { AgentCertificatePolicy.parse(validity, interval) }

/** C1, rule "Срок сертификата агента настраивается и не выходит за срок CA": the two settings. */
@MutFlowTest
class AgentCertificatePolicyTest {
    @Test
    fun `Без настройки сертификат агента выдаётся ровно на 365 суток`() {
        val defaults = policy()
        assertEquals(Duration.ofDays(365), defaults.validity)
        assertEquals(Duration.ofHours(1), defaults.renewalMinInterval)
    }

    @Test
    fun `Срок ровно 10 минут допустим`() {
        assertEquals(Duration.ofMinutes(10), policy(validity = "PT10M").validity)
    }

    @Test
    fun `Недопустимый срок сертификата агента не даёт серверу стартовать`() {
        for (value in listOf("PT9M59S", "PT0S", "-P1D", "year")) {
            val failure = assertFailsWith<InvalidPkiSettingException>(value) { policy(validity = value) }
            val message = failure.message.orEmpty()
            assertTrue(VALIDITY_SETTING in message, message)
            assertTrue(value in message, message)
            assertTrue("PT10M" in message, message)
        }
    }

    @Test
    fun `Недопустимый интервал продления не даёт серверу стартовать`() {
        for (value in listOf("PT0S", "-PT1H", "hour")) {
            val failure = assertFailsWith<InvalidPkiSettingException>(value) { policy(interval = value) }
            val message = failure.message.orEmpty()
            assertTrue(INTERVAL_SETTING in message, message)
            assertTrue(value in message, message)
        }
    }

    @Test
    fun `Самый малый положительный интервал допустим`() {
        assertEquals(Duration.ofSeconds(1), policy(interval = "PT1S").renewalMinInterval)
    }

    @Test
    fun `Действующий интервал это меньшее из настройки и десятой доли срока`() {
        val cases =
            listOf(
                Triple("P365D", "PT1H", Duration.ofHours(1)),
                Triple("PT10M", "PT1H", Duration.ofMinutes(1)),
                Triple("PT5H", "PT1H", Duration.ofMinutes(30)),
                Triple("P365D", "PT5M", Duration.ofMinutes(5)),
            )
        for ((validity, interval, expected) in cases) {
            assertEquals(expected, policy(validity, interval).effectiveInterval, "$validity $interval")
        }
    }
}
