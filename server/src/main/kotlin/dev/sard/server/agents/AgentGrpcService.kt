// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents

import dev.sard.proto.agent.v1.AgentServiceGrpcKt
import dev.sard.proto.agent.v1.ConnectRequest
import dev.sard.proto.agent.v1.ConnectResponse
import dev.sard.server.agents.stream.AgentStreams
import kotlinx.coroutines.flow.Flow
import org.springframework.grpc.server.service.GrpcService

/**
 * gRPC endpoint agents dial into. Connect is the stream manager (S5a); the other methods
 * answer UNIMPLEMENTED (inherited) until S4a.
 */
@GrpcService
class AgentGrpcService(
    private val streams: AgentStreams,
) : AgentServiceGrpcKt.AgentServiceCoroutineImplBase() {
    override fun connect(requests: Flow<ConnectRequest>): Flow<ConnectResponse> = streams.connect(requests)
}
