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

/**
 * The gRPC code half of the rejection contract table (docs/specs/server/agent-enrollment.feature),
 * grouped by outcome rather than spelled out one [Reason] per line: an exhaustive `when` (no
 * `else`), so a new [Reason] fails the build instead of silently defaulting at runtime. The wire
 * reason string itself is [Reason.name] (see [EnrollmentStatus.of]): the two always agree by
 * construction, and [EnrollmentStatusTest] pins every one of them literally, so a rename that
 * changed the wire contract would fail a test, never pass silently.
 */
private fun codeOf(reason: Reason): Status.Code =
    when (reason) {
        Reason.TOKEN_MALFORMED,
        Reason.HOSTNAME_INVALID,
        Reason.CSR_INVALID,
        -> Status.Code.INVALID_ARGUMENT

        Reason.TOKEN_FOREIGN_CA,
        Reason.TOKEN_UNKNOWN,
        Reason.TOKEN_USED,
        Reason.TOKEN_REVOKED,
        Reason.TOKEN_EXPIRED,
        -> Status.Code.UNAUTHENTICATED

        Reason.INTERNAL_RETRYABLE -> Status.Code.UNAVAILABLE
    }

/**
 * The one place enrollment errors become gRPC statuses (rejection contract,
 * docs/specs/server/agent-enrollment.feature): every rejection carries exactly one
 * `google.rpc.ErrorInfo` naming its reason, domain "sard.dev"; the status text never
 * repeats an internal cause.
 */
object EnrollmentStatus {
    fun of(error: Throwable): StatusRuntimeException {
        val reason = reasonOf(error)
        val info =
            ErrorInfo
                .newBuilder()
                .setReason(reason.name)
                .setDomain(DOMAIN)
                .build()
        val status =
            RpcStatus
                .newBuilder()
                .setCode(codeOf(reason).value())
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
