// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.notify

import dev.sard.server.verify.VerificationStatus
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertEquals

private fun notice(
    status: VerificationStatus,
    previous: VerificationStatus? = null,
    closedStaleEpisode: Boolean = false,
) = VerificationNotice(
    tenantId = UUID(0, 1),
    verificationId = UUID(0, 2),
    sourceName = "db-main",
    status = status,
    level = 0,
    reason = null,
    snapshotId = "4f3a9c1e7b2d",
    verifierHostname = "vrf1.example.com",
    stages = emptyList(),
    mismatches = emptyList(),
    restoreMillis = null,
    previous = previous,
    closedStaleEpisode = closedStaleEpisode,
)

/** V6, ОВ11а and ОВ11б: which verification records are told, as a pure function of the record and what came before. */
@MutFlowTest
class VerificationTellingTest {
    private fun telling(notice: VerificationNotice) = MutFlow.underTest { VerificationTelling.of(notice) }

    @Test
    fun `the first failure of a series is told, the second one is not`() {
        assertEquals(VerificationTelling.FIRST_FAILURE, telling(notice(VerificationStatus.FAILED)))
        assertEquals(
            VerificationTelling.FIRST_FAILURE,
            telling(notice(VerificationStatus.FAILED, previous = VerificationStatus.PASSED)),
        )
        assertEquals(null, telling(notice(VerificationStatus.FAILED, previous = VerificationStatus.FAILED)))
    }

    @Test
    fun `a pass after failures is told as a recovery, a pass after a pass is not`() {
        assertEquals(
            VerificationTelling.RECOVERY,
            telling(notice(VerificationStatus.PASSED, previous = VerificationStatus.FAILED)),
        )
        assertEquals(null, telling(notice(VerificationStatus.PASSED, previous = VerificationStatus.PASSED)))
        assertEquals(null, telling(notice(VerificationStatus.PASSED)))
    }

    @Test
    fun `a pass that closed a stale episode is told as a recovery`() {
        val closing = notice(VerificationStatus.PASSED, closedStaleEpisode = true)

        assertEquals(VerificationTelling.RECOVERY, telling(closing))
        assertEquals(
            VerificationTelling.RECOVERY,
            telling(notice(VerificationStatus.PASSED, previous = VerificationStatus.FAILED, closedStaleEpisode = true)),
        )
    }

    @Test
    fun `a verification that did not take place is never told`() {
        val silent =
            listOf(
                VerificationStatus.CANNOT_VERIFY,
                VerificationStatus.INFRASTRUCTURE_FAILURE,
                VerificationStatus.NO_VERIFIER,
            )
        for (status in silent) {
            assertEquals(null, telling(notice(status, previous = VerificationStatus.FAILED, closedStaleEpisode = true)))
        }
    }
}
