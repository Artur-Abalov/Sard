// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents

import dev.sard.proto.agent.v1.AgentServiceGrpcKt
import org.springframework.grpc.server.service.GrpcService

/**
 * gRPC endpoint agents dial into. Every method answers UNIMPLEMENTED (inherited)
 * until enrollment exists (roadmap: agent enrollment and mTLS certificates, stage 1).
 */
@GrpcService
class AgentGrpcService : AgentServiceGrpcKt.AgentServiceCoroutineImplBase()
