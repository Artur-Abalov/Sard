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
import io.grpc.Status.Code.INVALID_ARGUMENT
import io.grpc.Status.Code.UNAUTHENTICATED
import io.grpc.Status.Code.UNAVAILABLE
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

    /**
     * The literal wire string is hardcoded here, not derived from [Reason.name]: this pins the
     * rejection contract table independently of the domain enum (F2 review, EnrollmentStatus.kt).
     */
    private fun assertRejected(
        reason: Reason,
        code: Status.Code,
        wireReason: String,
    ) = assertMapping(EnrollmentRejectedException(reason), code, wireReason)

    @Test
    fun `TOKEN_MALFORMED is INVALID_ARGUMENT`() {
        assertRejected(Reason.TOKEN_MALFORMED, INVALID_ARGUMENT, "TOKEN_MALFORMED")
    }

    @Test
    fun `TOKEN_FOREIGN_CA is UNAUTHENTICATED`() {
        assertRejected(Reason.TOKEN_FOREIGN_CA, UNAUTHENTICATED, "TOKEN_FOREIGN_CA")
    }

    @Test
    fun `TOKEN_UNKNOWN is UNAUTHENTICATED`() {
        assertRejected(Reason.TOKEN_UNKNOWN, UNAUTHENTICATED, "TOKEN_UNKNOWN")
    }

    @Test
    fun `TOKEN_USED is UNAUTHENTICATED`() {
        assertRejected(Reason.TOKEN_USED, UNAUTHENTICATED, "TOKEN_USED")
    }

    @Test
    fun `TOKEN_REVOKED is UNAUTHENTICATED`() {
        assertRejected(Reason.TOKEN_REVOKED, UNAUTHENTICATED, "TOKEN_REVOKED")
    }

    @Test
    fun `TOKEN_EXPIRED is UNAUTHENTICATED`() {
        assertRejected(Reason.TOKEN_EXPIRED, UNAUTHENTICATED, "TOKEN_EXPIRED")
    }

    @Test
    fun `HOSTNAME_INVALID is INVALID_ARGUMENT`() {
        assertRejected(Reason.HOSTNAME_INVALID, INVALID_ARGUMENT, "HOSTNAME_INVALID")
    }

    @Test
    fun `CSR_INVALID is INVALID_ARGUMENT`() {
        assertRejected(Reason.CSR_INVALID, INVALID_ARGUMENT, "CSR_INVALID")
    }

    @Test
    fun `INTERNAL_RETRYABLE is UNAVAILABLE`() {
        assertRejected(Reason.INTERNAL_RETRYABLE, UNAVAILABLE, "INTERNAL_RETRYABLE")
    }

    @Test
    fun `an unrecognised throwable also maps to INTERNAL_RETRYABLE`() {
        assertMapping(IllegalStateException("boom"), UNAVAILABLE, "INTERNAL_RETRYABLE")
    }

    @Test
    fun `a CSR rejected outside Enrollment still maps to CSR_INVALID`() {
        assertMapping(InvalidCsrException("bad csr"), INVALID_ARGUMENT, "CSR_INVALID")
    }

    @Test
    fun `the status text never contains the cause's message`() {
        val cause = IllegalStateException("disk on fire")
        val exception = exceptionFor(EnrollmentRejectedException(Reason.INTERNAL_RETRYABLE, cause))
        val text = Status.fromThrowable(exception).description.orEmpty()
        assertEquals(false, "disk on fire" in text)
    }
}
