// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents

import dev.sard.server.agents.AgentAuthResult.Accepted
import dev.sard.server.agents.AgentAuthResult.Rejected
import io.grpc.Context
import io.grpc.Contexts
import io.grpc.Grpc
import io.grpc.Metadata
import io.grpc.ServerCall
import io.grpc.ServerCallHandler
import io.grpc.ServerInterceptor
import org.slf4j.LoggerFactory
import java.security.cert.X509Certificate
import javax.net.ssl.SSLPeerUnverifiedException

private val log = LoggerFactory.getLogger(AgentAuthInterceptor::class.java)

/**
 * Authenticates every gRPC call by its client certificate (ADR 0009). Deny by default:
 * only the services in [open] are reachable without one; any other service, including
 * one added later, needs the certificate of a live agent. The principal is checked once
 * when a call (or stream) starts and stays in its [Context] until it ends.
 */
class AgentAuthInterceptor(
    private val authenticator: AgentAuthenticator,
    private val open: Set<String>,
) : ServerInterceptor {
    override fun <Q, R> interceptCall(
        call: ServerCall<Q, R>,
        headers: Metadata,
        next: ServerCallHandler<Q, R>,
    ): ServerCall.Listener<Q> {
        if (call.methodDescriptor.serviceName in open) return next.startCall(call, headers)
        return when (val result = authenticator.authenticate(presented(call))) {
            is Accepted -> {
                val context = Context.current().withValue(AgentPrincipal.KEY, result.principal)
                Contexts.interceptCall(context, call, headers, next)
            }

            is Rejected -> {
                // Serial and agent id only: certificates and keys are never logged.
                log.warn(
                    "agent call refused: reason={} serial={} agent={} method={}",
                    result.failure,
                    result.serial,
                    result.agentId,
                    call.methodDescriptor.fullMethodName,
                )
                val error = AgentAuthStatus.of(result.failure)
                call.close(error.status, error.trailers ?: Metadata())
                object : ServerCall.Listener<Q>() {}
            }
        }
    }

    /** The leaf the TLS layer verified against the Sard CA, or null when none was presented. */
    private fun presented(call: ServerCall<*, *>): PresentedCertificate? {
        val session = call.attributes.get(Grpc.TRANSPORT_ATTR_SSL_SESSION) ?: return null
        val leaf =
            try {
                session.peerCertificates.firstOrNull() as? X509Certificate
            } catch (_: SSLPeerUnverifiedException) {
                null
            }
        return leaf?.let(PresentedCertificate::of)
    }
}
