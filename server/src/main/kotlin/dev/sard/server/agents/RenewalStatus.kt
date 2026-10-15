// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents

import com.google.protobuf.Any
import com.google.protobuf.Duration
import com.google.rpc.ErrorInfo
import com.google.rpc.RetryInfo
import dev.sard.server.enrollment.RenewalRejectedException
import io.grpc.Status
import io.grpc.StatusRuntimeException
import io.grpc.protobuf.StatusProto
import com.google.rpc.Status as RpcStatus

private typealias RenewalReason = RenewalRejectedException.Reason

/** Exhaustive `when`, no `else`: a new reason fails the build instead of defaulting at runtime. */
private fun codeOf(reason: RenewalReason): Status.Code =
    when (reason) {
        RenewalReason.CSR_INVALID,
        RenewalReason.CSR_KEY_REUSED,
        -> Status.Code.INVALID_ARGUMENT

        RenewalReason.RENEWAL_TOO_FREQUENT -> Status.Code.RESOURCE_EXHAUSTED

        RenewalReason.INTERNAL_RETRYABLE -> Status.Code.UNAVAILABLE
    }

/**
 * The one place a refused renewal becomes a gRPC status (ADR 0025): exactly one `google.rpc.ErrorInfo` with the
 * reason in the domain "sard.dev"; a renewal that came too often also carries `google.rpc.RetryInfo`.
 */
object RenewalStatus {
    fun of(error: Throwable): StatusRuntimeException {
        val rejected = error as? RenewalRejectedException ?: RenewalRejectedException(RenewalReason.INTERNAL_RETRYABLE, cause = error)
        val info =
            ErrorInfo
                .newBuilder()
                .setReason(rejected.reason.name)
                .setDomain(ERROR_DOMAIN)
                .build()
        val status = RpcStatus.newBuilder().setCode(codeOf(rejected.reason).value()).addDetails(Any.pack(info))
        rejected.retryAfter?.let { status.addDetails(Any.pack(retryInfo(it))) }
        return StatusProto.toStatusRuntimeException(status.build())
    }

    private fun retryInfo(delay: java.time.Duration): RetryInfo =
        RetryInfo
            .newBuilder()
            .setRetryDelay(Duration.newBuilder().setSeconds(delay.seconds).setNanos(delay.nano))
            .build()
}
