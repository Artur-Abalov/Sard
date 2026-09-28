// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents

import dev.sard.proto.agent.v1.AgentServiceGrpcKt
import dev.sard.proto.agent.v1.RegisterRequest
import dev.sard.proto.agent.v1.RegisterResponse
import dev.sard.server.enrollment.AgentEndpointProperties
import dev.sard.server.registration.Registration
import dev.sard.server.registration.RegistrationRejectedException
import dev.sard.server.registration.RegistrationRejectedException.Reason
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.withContext
import org.slf4j.LoggerFactory
import org.springframework.beans.factory.annotation.Qualifier
import org.springframework.grpc.server.service.GrpcService
import com.google.protobuf.Duration as ProtoDuration

private val log = LoggerFactory.getLogger(AgentGrpcService::class.java)

/**
 * gRPC endpoint agents dial into, behind the S3 interceptor: the agent is always the one of
 * the call's certificate. Register is implemented (S4a); Connect and RenewCertificate still
 * answer UNIMPLEMENTED (inherited). Blocking database work runs on [dispatcher]. Request
 * values are never logged: a refusal logs its reason and field only.
 */
@GrpcService
class AgentGrpcService(
    private val registration: Registration,
    private val agentProperties: AgentEndpointProperties,
    @param:Qualifier("agentServiceDispatcher") private val dispatcher: CoroutineDispatcher,
) : AgentServiceGrpcKt.AgentServiceCoroutineImplBase() {
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
