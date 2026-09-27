// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents

import com.google.protobuf.Any
import com.google.rpc.ErrorInfo
import dev.sard.server.enrollment.EnrollmentRejectedException
import dev.sard.server.pki.InvalidCsrException
import io.grpc.Status
import io.grpc.StatusRuntimeException
import io.grpc.protobuf.StatusProto
import com.google.rpc.Status as RpcStatus

private typealias Reason = EnrollmentRejectedException.Reason

/** google.rpc.ErrorInfo domain for every Enroll rejection (rejection contract). */
private const val DOMAIN = "sard.dev"

private val CODES =
    mapOf(
        Reason.TOKEN_MALFORMED to Status.Code.INVALID_ARGUMENT,
        Reason.TOKEN_FOREIGN_CA to Status.Code.UNAUTHENTICATED,
        Reason.TOKEN_UNKNOWN to Status.Code.UNAUTHENTICATED,
        Reason.TOKEN_USED to Status.Code.UNAUTHENTICATED,
        Reason.TOKEN_REVOKED to Status.Code.UNAUTHENTICATED,
        Reason.TOKEN_EXPIRED to Status.Code.UNAUTHENTICATED,
        Reason.HOSTNAME_INVALID to Status.Code.INVALID_ARGUMENT,
        Reason.CSR_INVALID to Status.Code.INVALID_ARGUMENT,
        Reason.INTERNAL_RETRYABLE to Status.Code.UNAVAILABLE,
    )

/**
 * The one place enrollment errors become gRPC statuses (rejection contract,
 * docs/specs/server/agent-enrollment.feature): every rejection carries exactly one
 * `google.rpc.ErrorInfo` naming its reason, domain "sard.dev"; the status text never
 * repeats an internal cause.
 */
object EnrollmentStatus {
    fun of(error: Throwable): StatusRuntimeException {
        val reason = reasonOf(error)
        val code = CODES.getValue(reason)
        val info =
            ErrorInfo
                .newBuilder()
                .setReason(reason.name)
                .setDomain(DOMAIN)
                .build()
        val status =
            RpcStatus
                .newBuilder()
                .setCode(code.value())
                .addDetails(Any.pack(info))
                .build()
        return StatusProto.toStatusRuntimeException(status)
    }

    private fun reasonOf(error: Throwable): Reason =
        when (error) {
            is EnrollmentRejectedException -> error.reason
            is InvalidCsrException -> Reason.CSR_INVALID
            else -> Reason.INTERNAL_RETRYABLE
        }
}
