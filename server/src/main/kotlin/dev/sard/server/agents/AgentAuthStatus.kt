// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents

import com.google.protobuf.Any
import com.google.rpc.Code
import com.google.rpc.ErrorInfo
import io.grpc.StatusRuntimeException
import io.grpc.protobuf.StatusProto

/** The error domain of Sard's machine-readable reasons (google.rpc.ErrorInfo). */
const val ERROR_DOMAIN = "sard.dev"

/** The one place a refused agent call becomes a gRPC status (ADR 0009). */
object AgentAuthStatus {
    fun of(failure: AgentAuthFailure): StatusRuntimeException {
        val info =
            ErrorInfo
                .newBuilder()
                .setReason(failure.name)
                .setDomain(ERROR_DOMAIN)
                .build()
        val status =
            com.google.rpc.Status
                .newBuilder()
                .setCode(Code.UNAUTHENTICATED_VALUE)
                .setMessage("agent certificate rejected")
                .addDetails(Any.pack(info))
                .build()
        return StatusProto.toStatusRuntimeException(status)
    }
}
