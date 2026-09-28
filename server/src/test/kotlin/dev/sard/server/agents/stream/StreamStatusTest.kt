// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents.stream

import com.google.rpc.ErrorInfo
import io.grpc.Metadata
import io.grpc.Status
import io.grpc.StatusRuntimeException
import io.grpc.protobuf.StatusProto
import kotlin.test.Test
import kotlin.test.assertEquals

/** The reasons are wire strings the agent and the console may match on: pinned as literals. */
class StreamStatusTest {
    private fun wire(e: StatusRuntimeException): Pair<Status.Code, String> {
        val info =
            StatusProto
                .fromStatusAndTrailers(e.status, e.trailers ?: Metadata())
                .detailsList
                .single()
                .unpack(ErrorInfo::class.java)
        assertEquals("sard.dev", info.domain)
        return e.status.code to info.reason
    }

    @Test
    fun `each reason closes the stream with its code and its name as the ErrorInfo reason`() {
        val expected =
            mapOf(
                StreamCloseReason.HELLO_REQUIRED to (Status.Code.FAILED_PRECONDITION to "HELLO_REQUIRED"),
                StreamCloseReason.AGENT_DUPLICATE_SESSION to (Status.Code.ALREADY_EXISTS to "AGENT_DUPLICATE_SESSION"),
                StreamCloseReason.SESSION_REPLACED to (Status.Code.UNAVAILABLE to "SESSION_REPLACED"),
                StreamCloseReason.SESSION_EXPIRED to (Status.Code.UNAVAILABLE to "SESSION_EXPIRED"),
                StreamCloseReason.SERVER_SHUTTING_DOWN to (Status.Code.UNAVAILABLE to "SERVER_SHUTTING_DOWN"),
            )
        assertEquals(StreamCloseReason.entries.toSet(), expected.keys)
        for ((reason, wire) in expected) {
            val close = reason.close()
            assertEquals(wire.second, close.reason)
            assertEquals(wire, wire(close.status), reason.name)
        }
    }

    @Test
    fun `the status message names the reason and nothing from the stream`() {
        val status =
            StreamCloseReason.SESSION_EXPIRED
                .close()
                .status.status
        assertEquals("stream closed: SESSION_EXPIRED", status.description)
    }
}
