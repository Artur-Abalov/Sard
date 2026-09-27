// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents

import dev.sard.proto.agent.v1.EnrollRequest
import dev.sard.proto.agent.v1.EnrollResponse
import dev.sard.proto.agent.v1.EnrollmentServiceGrpcKt
import dev.sard.server.enrollment.Enrollment
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.withContext
import org.slf4j.LoggerFactory
import org.springframework.beans.factory.annotation.Qualifier
import org.springframework.grpc.server.service.GrpcService
import kotlin.coroutines.cancellation.CancellationException

private val log = LoggerFactory.getLogger(EnrollmentGrpcService::class.java)

/**
 * Issues agent client certificates for one-time enrollment tokens (ADR 0009: server
 * TLS only, the token names the tenant). The blocking database and CA work runs on
 * [dispatcher].
 */
@GrpcService
class EnrollmentGrpcService(
    private val enrollment: Enrollment,
    @param:Qualifier("enrollmentDispatcher") private val dispatcher: CoroutineDispatcher,
) : EnrollmentServiceGrpcKt.EnrollmentServiceCoroutineImplBase() {
    override suspend fun enroll(request: EnrollRequest): EnrollResponse {
        val enrolled =
            runCatching {
                withContext(dispatcher) {
                    enrollment.enroll(request.enrollmentToken, request.csrDer.toByteArray(), request.hostname)
                }
            }.getOrElse { error ->
                if (error is CancellationException) throw error
                val status = EnrollmentStatus.of(error)
                // The request is never logged: it carries the token.
                if (status.code == io.grpc.Status.Code.INTERNAL) log.error("enrollment failed", error)
                throw status.asException()
            }
        return EnrollResponse
            .newBuilder()
            .setAgentId(enrolled.agentId.toString())
            .setCertificateChainPem(enrolled.certificateChainPem)
            .setCaBundlePem(enrolled.caBundlePem)
            .build()
    }
}
