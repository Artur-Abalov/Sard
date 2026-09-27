// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents

import dev.sard.proto.agent.v1.EnrollmentServiceGrpcKt
import org.springframework.grpc.server.service.GrpcService

/**
 * Issues agent client certificates for one-time enrollment tokens. Answers
 * UNIMPLEMENTED (inherited) until the certificate authority exists
 * (roadmap: agent enrollment and mTLS certificates, stage 1; ADR 0009).
 */
@GrpcService
class EnrollmentGrpcService : EnrollmentServiceGrpcKt.EnrollmentServiceCoroutineImplBase()
