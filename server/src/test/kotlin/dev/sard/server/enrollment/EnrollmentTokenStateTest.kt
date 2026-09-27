// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.enrollment

import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import java.time.Instant
import kotlin.test.Test
import kotlin.test.assertEquals

private val CREATED: Instant = Instant.parse("2026-10-01T12:00:00Z")
private val EXPIRES: Instant = Instant.parse("2026-10-01T13:00:00Z")

/** Rule: state is computed at read time, priority used > revoked > expired > active (decisions 6, 9). */
@MutFlowTest
class EnrollmentTokenStateTest {
    private fun stateAt(
        now: Instant,
        usedAt: Instant? = null,
        revokedAt: Instant? = null,
    ) = MutFlow.underTest { EnrollmentTokenState.of(usedAt, revokedAt, EXPIRES, now) }

    @Test
    fun `Токен за миллисекунду до срока активен`() {
        assertEquals(EnrollmentTokenState.ACTIVE, stateAt(EXPIRES.minusMillis(1)))
    }

    @Test
    fun `Токен в момент срока действия истёк`() {
        assertEquals(EnrollmentTokenState.EXPIRED, stateAt(EXPIRES))
    }

    @Test
    fun `Использованный токен после срока остаётся использованным`() {
        assertEquals(EnrollmentTokenState.USED, stateAt(EXPIRES.plusSeconds(1), usedAt = CREATED.plusSeconds(1)))
    }

    @Test
    fun `Отозванный токен после срока остаётся отозванным`() {
        assertEquals(EnrollmentTokenState.REVOKED, stateAt(EXPIRES.plusSeconds(1), revokedAt = CREATED.plusSeconds(1)))
    }

    @Test
    fun `used outranks revoked`() {
        val state = stateAt(EXPIRES.plusSeconds(1), usedAt = CREATED.plusSeconds(1), revokedAt = CREATED.plusSeconds(2))
        assertEquals(EnrollmentTokenState.USED, state)
    }
}
