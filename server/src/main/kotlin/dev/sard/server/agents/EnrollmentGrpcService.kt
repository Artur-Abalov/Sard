// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents

import dev.sard.proto.agent.v1.EnrollRequest
import dev.sard.proto.agent.v1.EnrollResponse
import dev.sard.proto.agent.v1.EnrollmentServiceGrpcKt
import dev.sard.server.enrollment.Enrollment
import io.grpc.Status
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.Job
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.withContext
import org.slf4j.LoggerFactory
import org.springframework.beans.factory.annotation.Qualifier
import org.springframework.grpc.server.service.GrpcService
import kotlin.coroutines.cancellation.CancellationException

private val log = LoggerFactory.getLogger(EnrollmentGrpcService::class.java)

/**
 * Issues agent client certificates for one-time enrollment tokens (ADR 0009: server
 * TLS only, the token names the tenant). The blocking database and CA work runs on
 * [dispatcher]. The request itself is never logged: it carries the token.
 */
@GrpcService
class EnrollmentGrpcService(
    private val enrollment: Enrollment,
    @param:Qualifier("enrollmentDispatcher") private val dispatcher: CoroutineDispatcher,
) : EnrollmentServiceGrpcKt.EnrollmentServiceCoroutineImplBase() {
    override suspend fun enroll(request: EnrollRequest): EnrollResponse {
        val job = currentCoroutineContext()[Job]
        val enrolled =
            runCatching {
                withContext(dispatcher) {
                    enrollment.enroll(request.enrollmentToken, request.csrDer.toByteArray(), request.hostname) {
                        job?.isActive == false
                    }
                }
            }.getOrElse { error -> throw translate(error) }
        return EnrollResponse
            .newBuilder()
            .setAgentId(enrolled.agentId.toString())
            .setCertificateChainPem(enrolled.certificateChainPem)
            .setCaBundlePem(enrolled.caBundlePem)
            .build()
    }

    private fun translate(error: Throwable): Throwable {
        if (error is CancellationException) return error
        val status = EnrollmentStatus.of(error)
        if (status.status.code == Status.Code.UNAVAILABLE) log.error("enrollment failed", error)
        return status
    }
}
