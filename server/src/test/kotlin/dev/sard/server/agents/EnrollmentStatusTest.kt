// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents

import com.google.protobuf.Any
import com.google.rpc.ErrorInfo
import dev.sard.server.enrollment.EnrollmentRejectedException
import dev.sard.server.pki.InvalidCsrException
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import io.grpc.Status
import io.grpc.StatusRuntimeException
import io.grpc.protobuf.StatusProto
import kotlin.test.Test
import kotlin.test.assertEquals

private typealias Reason = EnrollmentRejectedException.Reason

/** Rule "Отказы по токену — окончательный контракт": reason -> code, always with an ErrorInfo of domain sard.dev. */
@MutFlowTest
class EnrollmentStatusTest {
    private fun exceptionFor(error: Throwable): StatusRuntimeException {
        val exception = MutFlow.underTest { EnrollmentStatus.of(error) }
        return exception
    }

    private fun errorInfo(exception: StatusRuntimeException): ErrorInfo {
        val status = checkNotNull(StatusProto.fromThrowable(exception))
        val details = status.detailsList.filter { it.`is`(ErrorInfo::class.java) }
        assertEquals(1, details.size, status.detailsList.toString())
        return details.single().unpack(ErrorInfo::class.java)
    }

    private fun assertMapping(
        error: Throwable,
        code: Status.Code,
        reason: String,
    ) {
        val exception = exceptionFor(error)
        assertEquals(code, Status.fromThrowable(exception).code)
        val info = errorInfo(exception)
        assertEquals(reason, info.reason)
        assertEquals("sard.dev", info.domain)
    }

    private fun assertRejected(
        reason: Reason,
        code: Status.Code,
    ) = assertMapping(EnrollmentRejectedException(reason), code, reason.name)

    @Test
    fun `TOKEN_MALFORMED is INVALID_ARGUMENT`() = assertRejected(Reason.TOKEN_MALFORMED, Status.Code.INVALID_ARGUMENT)

    @Test
    fun `TOKEN_FOREIGN_CA is UNAUTHENTICATED`() = assertRejected(Reason.TOKEN_FOREIGN_CA, Status.Code.UNAUTHENTICATED)

    @Test
    fun `TOKEN_UNKNOWN is UNAUTHENTICATED`() = assertRejected(Reason.TOKEN_UNKNOWN, Status.Code.UNAUTHENTICATED)

    @Test
    fun `TOKEN_USED is UNAUTHENTICATED`() = assertRejected(Reason.TOKEN_USED, Status.Code.UNAUTHENTICATED)

    @Test
    fun `TOKEN_REVOKED is UNAUTHENTICATED`() = assertRejected(Reason.TOKEN_REVOKED, Status.Code.UNAUTHENTICATED)

    @Test
    fun `TOKEN_EXPIRED is UNAUTHENTICATED`() = assertRejected(Reason.TOKEN_EXPIRED, Status.Code.UNAUTHENTICATED)

    @Test
    fun `HOSTNAME_INVALID is INVALID_ARGUMENT`() = assertRejected(Reason.HOSTNAME_INVALID, Status.Code.INVALID_ARGUMENT)

    @Test
    fun `CSR_INVALID is INVALID_ARGUMENT`() = assertRejected(Reason.CSR_INVALID, Status.Code.INVALID_ARGUMENT)

    @Test
    fun `INTERNAL_RETRYABLE is UNAVAILABLE`() = assertRejected(Reason.INTERNAL_RETRYABLE, Status.Code.UNAVAILABLE)

    @Test
    fun `an unrecognised throwable also maps to INTERNAL_RETRYABLE`() {
        assertMapping(IllegalStateException("boom"), Status.Code.UNAVAILABLE, "INTERNAL_RETRYABLE")
    }

    @Test
    fun `a CSR rejected outside Enrollment still maps to CSR_INVALID`() {
        assertMapping(InvalidCsrException("bad csr"), Status.Code.INVALID_ARGUMENT, "CSR_INVALID")
    }

    @Test
    fun `the status text never contains the cause's message`() {
        val cause = IllegalStateException("disk on fire")
        val exception = exceptionFor(EnrollmentRejectedException(Reason.INTERNAL_RETRYABLE, cause))
        val text = Status.fromThrowable(exception).description.orEmpty()
        assertEquals(false, "disk on fire" in text)
    }
}
