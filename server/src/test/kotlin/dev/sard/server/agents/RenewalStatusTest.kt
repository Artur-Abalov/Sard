// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents

import com.google.rpc.ErrorInfo
import com.google.rpc.RetryInfo
import dev.sard.server.enrollment.RenewalRejectedException
import dev.sard.server.enrollment.RenewalRejectedException.Reason
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import io.grpc.Status
import io.grpc.protobuf.StatusProto
import java.time.Duration
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNull

/** C1, ADR 0025: a refused renewal is one gRPC status with one ErrorInfo; only "too frequent" carries RetryInfo. */
@MutFlowTest
class RenewalStatusTest {
    private fun statusOf(error: Throwable) = MutFlow.underTest { RenewalStatus.of(error) }

    private fun detailsOf(error: Throwable) = StatusProto.fromThrowable(statusOf(error))!!.detailsList

    @Test
    fun `каждая причина отказа имеет свой код gRPC`() {
        val expected =
            mapOf(
                Reason.CSR_INVALID to Status.Code.INVALID_ARGUMENT,
                Reason.CSR_KEY_REUSED to Status.Code.INVALID_ARGUMENT,
                Reason.RENEWAL_TOO_FREQUENT to Status.Code.RESOURCE_EXHAUSTED,
                Reason.INTERNAL_RETRYABLE to Status.Code.UNAVAILABLE,
            )
        Reason.entries.forEach { reason ->
            assertEquals(expected.getValue(reason), statusOf(RenewalRejectedException(reason)).status.code, reason.name)
        }
    }

    @Test
    fun `отказ несёт ровно один ErrorInfo с причиной в домене sard dev`() {
        val details = detailsOf(RenewalRejectedException(Reason.CSR_KEY_REUSED))
        assertEquals(1, details.size)
        val info = details.single().unpack(ErrorInfo::class.java)
        assertEquals("CSR_KEY_REUSED", info.reason)
        assertEquals("sard.dev", info.domain)
    }

    @Test
    fun `слишком частое продление несёт RetryInfo с секундами и наносекундами`() {
        val details = detailsOf(RenewalRejectedException(Reason.RENEWAL_TOO_FREQUENT, Duration.ofSeconds(90, 500)))
        assertEquals(2, details.size)
        assertEquals("RENEWAL_TOO_FREQUENT", details[0].unpack(ErrorInfo::class.java).reason)
        val delay = details[1].unpack(RetryInfo::class.java).retryDelay
        assertEquals(90, delay.seconds)
        assertEquals(500, delay.nanos)
    }

    @Test
    fun `чужая ошибка становится INTERNAL_RETRYABLE без подробностей причины`() {
        val failure = statusOf(IllegalStateException("db is down"))
        assertEquals(Status.Code.UNAVAILABLE, failure.status.code)
        val details = StatusProto.fromThrowable(failure)!!.detailsList
        assertEquals(1, details.size)
        assertEquals("INTERNAL_RETRYABLE", details.single().unpack(ErrorInfo::class.java).reason)
        assertNull(failure.status.description?.takeIf { "db is down" in it })
    }
}
