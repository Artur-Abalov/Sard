// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents

import dev.sard.proto.agent.v1.AgentServiceGrpc
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

/** The one call a certificate that nothing has confirmed yet may make (C1, П8). */
private val CONFIRM_METHOD = AgentServiceGrpc.getConfirmCertificateMethod().fullMethodName

/** The public key of the certificate a call presented (SubjectPublicKeyInfo, DER): the renewal must not reuse it. */
object PresentedKey {
    val KEY: Context.Key<ByteArray> = Context.key("sard.presented-key")
}

private const val ROLE_HINT =
    "the certificate carries no valid role of the agent (it was issued before roles existed, or is not ours): " +
        "re-enroll the agent"

private const val UNKNOWN_CERTIFICATE_HINT =
    "the certificate was issued by this server's CA, but the database has no record of it: " +
        "the database was not restored or was restored from a copy older than the agent's enrollment"

/** The log line for a refused call; the caller sees the reason alone (ADR 0009, ADR 0052). */
internal fun refusalMessage(
    rejected: Rejected,
    method: String,
): String {
    val line = "agent call refused: reason=${rejected.failure} serial=${rejected.serial} agent=${rejected.agentId}"
    return when (rejected.failure) {
        AgentAuthFailure.CERT_UNKNOWN -> "$line tenant=${rejected.tenantId} method=$method: $UNKNOWN_CERTIFICATE_HINT"

        AgentAuthFailure.CERT_ROLE_MISSING,
        AgentAuthFailure.CERT_ROLE_INVALID,
        -> "$line tenant=${rejected.tenantId} method=$method: $ROLE_HINT"

        AgentAuthFailure.CERT_MISSING,
        AgentAuthFailure.CERT_IDENTITY_MISMATCH,
        AgentAuthFailure.CERT_REVOKED,
        AgentAuthFailure.CERT_SUPERSEDED,
        AgentAuthFailure.CERT_NOT_CONFIRMED,
        AgentAuthFailure.CERT_EXPIRED,
        AgentAuthFailure.AGENT_REVOKED,
        -> "$line method=$method"
    }
}

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
        val leaf = leaf(call)
        val presented = leaf?.let(PresentedCertificate::of)
        return when (val result = authenticator.authenticate(presented, scopeOf(call))) {
            is Accepted -> Contexts.interceptCall(contextOf(result.principal, leaf), call, headers, next)
            is Rejected -> refuse(result, call)
        }
    }

    /** Only the confirmation of a renewed certificate may be made with a certificate nothing has confirmed yet. */
    private fun scopeOf(call: ServerCall<*, *>): AuthScope =
        if (call.methodDescriptor.fullMethodName == CONFIRM_METHOD) AuthScope.CONFIRM else AuthScope.CALL

    private fun contextOf(
        principal: AgentPrincipal,
        leaf: X509Certificate?,
    ): Context =
        Context
            .current()
            .withValue(AgentPrincipal.KEY, principal)
            .withValue(PresentedKey.KEY, checkNotNull(leaf).publicKey.encoded)

    private fun <Q> refuse(
        rejected: Rejected,
        call: ServerCall<Q, *>,
    ): ServerCall.Listener<Q> {
        // Serial and agent id only: certificates and keys are never logged.
        log.warn(refusalMessage(rejected, call.methodDescriptor.fullMethodName))
        val error = AgentAuthStatus.of(rejected.failure)
        call.close(error.status, error.trailers ?: Metadata())
        return object : ServerCall.Listener<Q>() {}
    }

    /** The leaf the TLS layer verified against the Sard CA, or null when none was presented. */
    private fun leaf(call: ServerCall<*, *>): X509Certificate? {
        val session = call.attributes.get(Grpc.TRANSPORT_ATTR_SSL_SESSION) ?: return null
        return try {
            session.peerCertificates.firstOrNull() as? X509Certificate
        } catch (_: SSLPeerUnverifiedException) {
            null
        }
    }
}
