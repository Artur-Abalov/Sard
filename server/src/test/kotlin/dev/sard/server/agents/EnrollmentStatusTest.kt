// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents

import dev.sard.server.enrollment.EnrollmentRejectedException
import dev.sard.server.enrollment.EnrollmentRejectedException.Reason
import dev.sard.server.pki.InvalidCsrException
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import io.grpc.Status
import kotlin.test.Test
import kotlin.test.assertEquals

/** Interim mapping (S2a); S2b scenarios fix the final codes and texts. */
@MutFlowTest
class EnrollmentStatusTest {
    @Test
    fun `every token rejection is UNAUTHENTICATED and names its reason`() {
        for (reason in Reason.entries) {
            val status = MutFlow.underTest { EnrollmentStatus.of(EnrollmentRejectedException(reason)) }
            assertEquals(Status.Code.UNAUTHENTICATED, status.code, reason.name)
            assertEquals("enrollment token rejected: ${reason.name.lowercase()}", status.description)
        }
    }

    @Test
    fun `a CSR the CA refuses is INVALID_ARGUMENT with the CA's reason`() {
        val status = MutFlow.underTest { EnrollmentStatus.of(InvalidCsrException("CSR signature does not verify")) }
        assertEquals(Status.Code.INVALID_ARGUMENT, status.code)
        assertEquals("CSR signature does not verify", status.description)
    }

    @Test
    fun `anything else is INTERNAL and reveals nothing`() {
        val status = MutFlow.underTest { EnrollmentStatus.of(IllegalStateException("row sard_secret")) }
        assertEquals(Status.Code.INTERNAL, status.code)
        assertEquals("enrollment failed", status.description)
    }
}
