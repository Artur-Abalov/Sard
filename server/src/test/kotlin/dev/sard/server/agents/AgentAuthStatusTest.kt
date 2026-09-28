// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents

import com.google.rpc.ErrorInfo
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import io.grpc.Status
import io.grpc.protobuf.StatusProto
import kotlin.test.Test
import kotlin.test.assertEquals

/** ADR 0009: a refused agent call is UNAUTHENTICATED with the reason in google.rpc.ErrorInfo. */
@MutFlowTest
class AgentAuthStatusTest {
    @Test
    fun `every failure is UNAUTHENTICATED with its name as the reason in the sard dev domain`() {
        for (failure in AgentAuthFailure.entries) {
            val error = MutFlow.underTest { AgentAuthStatus.of(failure) }
            assertEquals(Status.Code.UNAUTHENTICATED, error.status.code, failure.name)
            assertEquals("agent certificate rejected", error.status.description)
            val details = StatusProto.fromStatusAndTrailers(error.status, error.trailers).detailsList
            val info = details.single().unpack(ErrorInfo::class.java)
            assertEquals(failure.name, info.reason)
            assertEquals("sard.dev", info.domain)
        }
    }
}
