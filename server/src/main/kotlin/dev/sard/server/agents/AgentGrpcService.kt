// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents

import dev.sard.proto.agent.v1.AgentServiceGrpcKt
import dev.sard.proto.agent.v1.ConfirmCertificateRequest
import dev.sard.proto.agent.v1.ConfirmCertificateResponse
import dev.sard.proto.agent.v1.ConnectRequest
import dev.sard.proto.agent.v1.ConnectResponse
import dev.sard.proto.agent.v1.RegisterRequest
import dev.sard.proto.agent.v1.RegisterResponse
import dev.sard.proto.agent.v1.RenewCertificateRequest
import dev.sard.proto.agent.v1.RenewCertificateResponse
import dev.sard.server.agents.stream.AgentStreams
import dev.sard.server.enrollment.AgentEndpointProperties
import dev.sard.server.enrollment.CertificateRenewal
import dev.sard.server.enrollment.RenewalCaller
import dev.sard.server.registration.Registration
import dev.sard.server.registration.RegistrationRejectedException
import dev.sard.server.registration.RegistrationRejectedException.Reason
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.withContext
import org.slf4j.LoggerFactory
import org.springframework.beans.factory.annotation.Qualifier
import org.springframework.grpc.server.service.GrpcService
import kotlin.coroutines.cancellation.CancellationException
import com.google.protobuf.Duration as ProtoDuration

private val log = LoggerFactory.getLogger(AgentGrpcService::class.java)

/**
 * gRPC endpoint agents dial into, behind the S3 interceptor: the agent is always the one of
 * the call's certificate. Register is implemented (S4a), Connect is the stream manager (S5a);
 * RenewCertificate and ConfirmCertificate renew the certificate (C1). Blocking database work runs on
 * [dispatcher]. Request values are never logged: a refusal logs its reason and field only.
 */
@GrpcService
class AgentGrpcService(
    private val registration: Registration,
    private val agentProperties: AgentEndpointProperties,
    private val renewal: CertificateRenewal,
    @param:Qualifier("agentServiceDispatcher") private val dispatcher: CoroutineDispatcher,
    private val streams: AgentStreams,
) : AgentServiceGrpcKt.AgentServiceCoroutineImplBase() {
    override fun connect(requests: Flow<ConnectRequest>): Flow<ConnectResponse> = streams.connect(requests)

    override suspend fun renewCertificate(request: RenewCertificateRequest): RenewCertificateResponse {
        val caller = callerOf()
        val csr = request.csrDer.toByteArray()
        val renewed = runRenewal { renewal.renew(caller, csr) }
        return RenewCertificateResponse.newBuilder().setCertificateChainPem(renewed.chainPem).build()
    }

    override suspend fun confirmCertificate(request: ConfirmCertificateRequest): ConfirmCertificateResponse {
        val caller = callerOf()
        runRenewal { renewal.confirm(caller) }
        return ConfirmCertificateResponse.getDefaultInstance()
    }

    private fun callerOf(): RenewalCaller {
        val principal = checkNotNull(AgentPrincipal.KEY.get()) { "a renewal outside an authenticated agent call" }
        val key = checkNotNull(PresentedKey.KEY.get()) { "a renewal outside an authenticated agent call" }
        return RenewalCaller(principal.tenantId, principal.agentId, principal.serial, key)
    }

    /** Runs the blocking work on [dispatcher]; a refusal becomes the status of ADR 0025, a cancellation passes. */
    private suspend fun <T> runRenewal(work: () -> T): T =
        runCatching { withContext(dispatcher) { work() } }.getOrElse { error ->
            if (error is CancellationException) throw error
            throw RenewalStatus.of(error)
        }

    override suspend fun register(request: RegisterRequest): RegisterResponse {
        val principal = checkNotNull(AgentPrincipal.KEY.get()) { "Register outside an authenticated agent call" }
        val snapshot = request.toSnapshot()
        try {
            withContext(dispatcher) { registration.register(principal.tenantId, principal.agentId, snapshot) }
        } catch (e: RegistrationRejectedException) {
            if (e.reason == Reason.INTERNAL_RETRYABLE) {
                log.error("register of agent {} failed", principal.agentId, e)
            } else {
                log.warn("register of agent {} rejected: {} {}", principal.agentId, e.reason, e.details)
            }
            throw RegistrationStatus.of(e)
        }
        return RegisterResponse
            .newBuilder()
            .setAgentId(principal.agentId.toString())
            .setHeartbeatInterval(protoDuration(agentProperties.heartbeatInterval))
            .build()
    }

    private fun protoDuration(duration: java.time.Duration): ProtoDuration =
        ProtoDuration
            .newBuilder()
            .setSeconds(duration.seconds)
            .setNanos(duration.nano)
            .build()
}
