// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents

import com.google.rpc.ErrorInfo
import dev.sard.proto.agent.v1.AgentServiceGrpc
import dev.sard.server.pki.PkiFixtures
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import io.grpc.Attributes
import io.grpc.Grpc
import io.grpc.Metadata
import io.grpc.MethodDescriptor
import io.grpc.ServerCall
import io.grpc.ServerCallHandler
import io.grpc.Status
import io.grpc.protobuf.StatusProto
import java.lang.reflect.Proxy
import java.security.KeyPairGenerator
import java.security.cert.Certificate
import java.security.spec.ECGenParameterSpec
import java.time.Clock
import javax.net.ssl.SSLSession
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNotNull
import kotlin.test.assertNull
import kotlin.test.assertTrue

private class ClosedCall(
    private val method: MethodDescriptor<*, *>,
) : ServerCall<Any, Any>() {
    var status: Status? = null
    var trailers: Metadata? = null

    override fun request(numMessages: Int) = Unit

    override fun sendHeaders(headers: Metadata) = Unit

    override fun sendMessage(message: Any) = Unit

    override fun close(
        status: Status,
        trailers: Metadata,
    ) {
        this.status = status
        this.trailers = trailers
    }

    override fun isCancelled() = false

    @Suppress("UNCHECKED_CAST")
    override fun getMethodDescriptor() = method as MethodDescriptor<Any, Any>

    override fun getAttributes(): Attributes =
        Attributes
            .newBuilder()
            .set(Grpc.TRANSPORT_ATTR_SSL_SESSION, session())
            .build()
}

/** A TLS session whose verified peer is some certificate: the interceptor only reads its serial. */
private fun session(): SSLSession {
    val keys =
        KeyPairGenerator
            .getInstance("EC")
            .apply { initialize(ECGenParameterSpec("secp256r1")) }
            .generateKeyPair()
    val peer = PkiFixtures.rootLike(keys, keys.private, ca = false)
    return Proxy.newProxyInstance(SSLSession::class.java.classLoader, arrayOf(SSLSession::class.java)) { _, _, _ ->
        arrayOf<Certificate>(peer)
    } as SSLSession
}

/** A8d-3 (ADR 0009 amendment): an unreachable database is a retryable server fault, never a refusal. */
@MutFlowTest
class AgentAuthUnavailableTest {
    private val broken = CertificateStandings { error("connection to db-secret-host refused") }
    private val method = AgentServiceGrpc.getProbeMethod()

    private fun intercept(call: ClosedCall): Boolean {
        var reached = false
        val next =
            ServerCallHandler<Any, Any> { _, _ ->
                reached = true
                object : ServerCall.Listener<Any>() {}
            }
        val interceptor = AgentAuthInterceptor(AgentAuthenticator(broken, Clock.systemUTC()), emptySet())
        MutFlow.underTest { interceptor.interceptCall(call, Metadata(), next) }
        return reached
    }

    @Test
    fun `an unavailable database closes the call UNAVAILABLE with INTERNAL_RETRYABLE`() {
        val call = ClosedCall(method)
        assertFalse(intercept(call))
        val status = assertNotNull(call.status)
        assertEquals(Status.Code.UNAVAILABLE, status.code)
        val rpc = StatusProto.fromStatusAndTrailers(status, call.trailers)
        val info = rpc.detailsList.single().unpack(ErrorInfo::class.java)
        assertEquals("INTERNAL_RETRYABLE", info.reason)
        assertEquals("sard.dev", info.domain)
        assertTrue(info.metadataMap.isEmpty())
    }

    @Test
    fun `the status does not disclose the internal error`() {
        val call = ClosedCall(method)
        intercept(call)
        val text = "${call.status} ${StatusProto.fromStatusAndTrailers(call.status!!, call.trailers)}"
        assertFalse("db-secret-host" in text, text)
        assertNull(call.status!!.cause)
    }
}
