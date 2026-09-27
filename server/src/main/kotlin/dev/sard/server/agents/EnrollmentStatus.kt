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

/** One entry per row of the rejection contract table: the gRPC code and the wire reason string. */
private data class Wire(
    val code: Status.Code,
    val reason: String,
)

/**
 * The rejection contract table (docs/specs/server/agent-enrollment.feature), spelled out as
 * literal wire strings rather than derived from [Reason.name]: the wire contract with agents is
 * pinned here on purpose, so renaming the domain enum can never silently change it.
 */
private val WIRE =
    mapOf(
        Reason.TOKEN_MALFORMED to Wire(Status.Code.INVALID_ARGUMENT, "TOKEN_MALFORMED"),
        Reason.TOKEN_FOREIGN_CA to Wire(Status.Code.UNAUTHENTICATED, "TOKEN_FOREIGN_CA"),
        Reason.TOKEN_UNKNOWN to Wire(Status.Code.UNAUTHENTICATED, "TOKEN_UNKNOWN"),
        Reason.TOKEN_USED to Wire(Status.Code.UNAUTHENTICATED, "TOKEN_USED"),
        Reason.TOKEN_REVOKED to Wire(Status.Code.UNAUTHENTICATED, "TOKEN_REVOKED"),
        Reason.TOKEN_EXPIRED to Wire(Status.Code.UNAUTHENTICATED, "TOKEN_EXPIRED"),
        Reason.HOSTNAME_INVALID to Wire(Status.Code.INVALID_ARGUMENT, "HOSTNAME_INVALID"),
        Reason.CSR_INVALID to Wire(Status.Code.INVALID_ARGUMENT, "CSR_INVALID"),
        Reason.INTERNAL_RETRYABLE to Wire(Status.Code.UNAVAILABLE, "INTERNAL_RETRYABLE"),
    )

/**
 * The one place enrollment errors become gRPC statuses (rejection contract,
 * docs/specs/server/agent-enrollment.feature): every rejection carries exactly one
 * `google.rpc.ErrorInfo` naming its reason, domain "sard.dev"; the status text never
 * repeats an internal cause.
 */
object EnrollmentStatus {
    fun of(error: Throwable): StatusRuntimeException {
        val wire = WIRE.getValue(reasonOf(error))
        val info =
            ErrorInfo
                .newBuilder()
                .setReason(wire.reason)
                .setDomain(DOMAIN)
                .build()
        val status =
            RpcStatus
                .newBuilder()
                .setCode(wire.code.value())
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
