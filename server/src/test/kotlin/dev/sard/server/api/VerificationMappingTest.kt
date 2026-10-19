// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import dev.sard.server.verify.JobReason
import dev.sard.server.verify.OpenJob
import dev.sard.server.verify.VerificationStatus
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import java.time.Instant
import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertEquals

/** The domain's verification values as the REST contract names them (V4). */
@MutFlowTest
class VerificationMappingTest {
    @Test
    fun `every verification status maps to the outcome of the same name`() {
        val expected =
            mapOf(
                VerificationStatus.PASSED to VerificationOutcome.PASSED,
                VerificationStatus.FAILED to VerificationOutcome.FAILED,
                VerificationStatus.CANNOT_VERIFY to VerificationOutcome.CANNOT_VERIFY,
                VerificationStatus.INFRASTRUCTURE_FAILURE to VerificationOutcome.INFRASTRUCTURE_FAILURE,
                VerificationStatus.NO_VERIFIER to VerificationOutcome.NO_VERIFIER,
            )
        assertEquals(expected, VerificationStatus.entries.associateWith { MutFlow.underTest { outcomeOf(it) } })
    }

    @Test
    fun `a running cycle job keeps its times, attempts and the reasons of each verifier`() {
        val unsuitable = """[{"agentId":"${UUID(0, 9)}","hostname":"v","reasons":["same_host","busy"]}]"""
        val job = OpenJob(UUID(0, 1), true, JobReason.CYCLE, Instant.EPOCH, Instant.MAX, 2, unsuitable)

        val mapped = MutFlow.underTest { jobOf(job) }

        val misfit = VerifierMisfit(UUID(0, 9), "v", listOf(VerifierReason.SAME_HOST, VerifierReason.BUSY))
        assertEquals(
            VerificationJob(
                UUID(0, 1),
                VerificationJobStatus.RUNNING,
                VerificationJobReason.CYCLE,
                Instant.EPOCH,
                Instant.MAX,
                2,
                listOf(misfit),
            ),
            mapped,
        )
    }

    @Test
    fun `a queued manual job not tried yet has no reasons`() {
        val job = OpenJob(UUID(0, 1), false, JobReason.MANUAL, Instant.EPOCH, Instant.EPOCH, 0, null)

        val mapped = MutFlow.underTest { jobOf(job) }

        assertEquals(
            listOf<Any?>(VerificationJobStatus.QUEUED, VerificationJobReason.MANUAL, null),
            listOf(mapped.status, mapped.reason, mapped.unsuitable),
        )
    }
}
