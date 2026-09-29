// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents.stream

import com.google.protobuf.Any
import com.google.rpc.ErrorInfo
import dev.sard.server.agents.ERROR_DOMAIN
import io.grpc.Status
import io.grpc.StatusRuntimeException
import io.grpc.protobuf.StatusProto

/**
 * Why the server ends a Connect stream (S5a); the name is the ErrorInfo reason, so renaming a
 * constant changes the contract. The agent (A3) reconnects after any of them.
 */
enum class StreamCloseReason(
    val code: Status.Code,
) {
    /** The first message was not Hello, or none came within the hello timeout. */
    HELLO_REQUIRED(Status.Code.FAILED_PRECONDITION),

    /** Another stream of the same agent is alive (D5: a cloned host). */
    AGENT_DUPLICATE_SESSION(Status.Code.ALREADY_EXISTS),

    /** A newer stream of the same agent took over a silent one. */
    SESSION_REPLACED(Status.Code.UNAVAILABLE),

    /** Nothing came from the agent for `offlineAfter`. */
    SESSION_EXPIRED(Status.Code.UNAVAILABLE),

    /** The server is stopping; the agent reconnects to its successor. */
    SERVER_SHUTTING_DOWN(Status.Code.UNAVAILABLE),
    ;

    fun close(): StreamClose = StreamClose(name) { status(code, name, "stream closed: $name") }
}

/**
 * A server-side end of a stream: the reason for logs and listeners, and a fresh status for the
 * agent on every [status] call. One close may end many streams (server shutdown), and gRPC
 * mutates a status' trailers while writing them; a shared, non-thread-safe [io.grpc.Metadata]
 * broke concurrent closes.
 */
class StreamClose(
    val reason: String,
    private val newStatus: () -> StatusRuntimeException,
) {
    fun status(): StatusRuntimeException = newStatus()
}

/** The ADR 0025 error shape: one ErrorInfo in domain `sard.dev`. */
internal fun status(
    code: Status.Code,
    reason: String,
    message: String,
): StatusRuntimeException {
    val info =
        ErrorInfo
            .newBuilder()
            .setReason(reason)
            .setDomain(ERROR_DOMAIN)
            .build()
    val status =
        com.google.rpc.Status
            .newBuilder()
            .setCode(code.value())
            .setMessage(message)
            .addDetails(Any.pack(info))
            .build()
    return StatusProto.toStatusRuntimeException(status)
}
