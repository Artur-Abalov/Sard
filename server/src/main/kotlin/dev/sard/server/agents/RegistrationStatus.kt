// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents

import com.google.protobuf.Any
import com.google.rpc.ErrorInfo
import dev.sard.server.registration.RegistrationRejectedException
import io.grpc.Status
import io.grpc.StatusRuntimeException
import io.grpc.protobuf.StatusProto
import com.google.rpc.Status as RpcStatus
import dev.sard.server.registration.RegistrationRejectedException.Reason as RegisterReason

/** Exhaustive, no `else`: a new [RegisterReason] fails the build until it has a code (gRPC error model ADR). */
private fun registerCodeOf(reason: RegisterReason): Status.Code =
    when (reason) {
        RegisterReason.PROTOCOL_UNSUPPORTED -> Status.Code.FAILED_PRECONDITION

        RegisterReason.HOSTNAME_INVALID,
        RegisterReason.FIELD_INVALID,
        RegisterReason.NAME_INVALID,
        RegisterReason.NAME_DUPLICATE,
        RegisterReason.SNAPSHOT_TOO_LARGE,
        RegisterReason.CONFIG_SCHEMA_INVALID,
        -> Status.Code.INVALID_ARGUMENT

        RegisterReason.INTERNAL_RETRYABLE -> Status.Code.UNAVAILABLE
    }

/**
 * The one place a refused Register becomes a gRPC status: exactly one ErrorInfo, domain
 * sard.dev, reason [RegisterReason.name], metadata naming the field and limit (never a request value).
 */
object RegistrationStatus {
    fun of(error: RegistrationRejectedException): StatusRuntimeException {
        val info =
            ErrorInfo
                .newBuilder()
                .setReason(error.reason.name)
                .setDomain(ERROR_DOMAIN)
                .putAllMetadata(error.details)
                .build()
        val status =
            RpcStatus
                .newBuilder()
                .setCode(registerCodeOf(error.reason).value())
                .setMessage("register rejected")
                .addDetails(Any.pack(info))
                .build()
        return StatusProto.toStatusRuntimeException(status)
    }
}
