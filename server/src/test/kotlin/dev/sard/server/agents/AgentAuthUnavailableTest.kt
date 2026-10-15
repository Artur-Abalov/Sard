// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents

import com.google.rpc.ErrorInfo
import dev.sard.proto.agent.v1.AgentServiceGrpc
import dev.sard.server.pki.AgentRoleUri
import dev.sard.server.pki.CaKeyPair
import dev.sard.server.pki.ForgedCertificates
import dev.sard.server.pki.ForgedRole
import dev.sard.server.pki.Keys
import dev.sard.server.pki.PkiFixtures
import dev.sard.server.pki.ROLE_URI_VECTORS
import dev.sard.server.role.AgentRole
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
import java.math.BigInteger
import java.security.KeyPairGenerator
import java.security.cert.Certificate
import java.security.cert.X509Certificate
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
    private val peer: X509Certificate = anyCertificate(),
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
            .set(Grpc.TRANSPORT_ATTR_SSL_SESSION, session(peer))
            .build()
}

private fun anyCertificate(): X509Certificate {
    val keys =
        KeyPairGenerator
            .getInstance("EC")
            .apply { initialize(ECGenParameterSpec("secp256r1")) }
            .generateKeyPair()
    return PkiFixtures.rootLike(keys, keys.private, ca = false)
}

/** A TLS session whose verified peer is [peer]. */
private fun session(peer: X509Certificate): SSLSession =
    Proxy.newProxyInstance(SSLSession::class.java.classLoader, arrayOf(SSLSession::class.java)) { _, _, _ ->
        arrayOf<Certificate>(peer)
    } as SSLSession

private const val HEX = 16
private const val DAY = 86_400L

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

    private val ca = CaKeyPair.generate(PkiFixtures.CLOCK, PkiFixtures.random())
    private val agentKeys = Keys.generate(PkiFixtures.random())
    private val serial = BigInteger.TEN
    private val standing =
        CertificateStanding(
            PkiFixtures.AGENT,
            notAfter = PkiFixtures.NOW.plusSeconds(DAY),
            revokedAt = null,
            agentRevokedAt = null,
            role = AgentRole.SOURCE,
        )

    /** What a call carrying a certificate with this role says is reached: the refusal reason, or the principal. */
    private data class Outcome(
        val reason: String?,
        val principal: AgentPrincipal?,
    )

    private fun callWith(role: ForgedRole): Outcome {
        val certificate = ForgedCertificates(ca).forge(agentKeys, serial, PkiFixtures.AGENT, role, PkiFixtures.NOW)
        val call = ClosedCall(method, certificate)
        var principal: AgentPrincipal? = null
        val next =
            ServerCallHandler<Any, Any> { _, _ ->
                principal = AgentPrincipal.KEY.get()
                object : ServerCall.Listener<Any>() {}
            }
        val standings = CertificateStandings { if (it == serial.toString(HEX)) standing else null }
        val interceptor = AgentAuthInterceptor(AgentAuthenticator(standings, PkiFixtures.CLOCK), emptySet())
        MutFlow.underTest { interceptor.interceptCall(call, Metadata(), next) }
        val reason =
            call.status?.let {
                StatusProto
                    .fromStatusAndTrailers(it, call.trailers)
                    .detailsList
                    .single()
                    .unpack(ErrorInfo::class.java)
                    .reason
            }
        return Outcome(reason, principal)
    }

    @Test
    fun `a certificate with the role URI of its record is accepted as that agent`() {
        val outcome = callWith(ForgedRole.Text("source"))
        assertNull(outcome.reason)
        val expected = AgentPrincipal(PkiFixtures.AGENT.agentId, PkiFixtures.AGENT.tenantId, "a", AgentRole.SOURCE)
        assertEquals(expected, outcome.principal)
    }

    @Test
    fun `a certificate with the other role than its record is refused CERT_IDENTITY_MISMATCH`() {
        assertEquals(Outcome("CERT_IDENTITY_MISMATCH", null), callWith(ForgedRole.Text("verifier")))
    }

    @Test
    fun `a certificate without a role URI is refused CERT_ROLE_MISSING`() {
        assertEquals(Outcome("CERT_ROLE_MISSING", null), callWith(ForgedRole.Absent))
    }

    @Test
    fun `a certificate with several role URIs is refused CERT_ROLE_INVALID`() {
        val twice = ForgedRole.Uris(listOf(AgentRoleUri.of(AgentRole.SOURCE), AgentRoleUri.of(AgentRole.SOURCE)))
        assertEquals(Outcome("CERT_ROLE_INVALID", null), callWith(twice))
    }

    @Test
    fun `the shared role URI vectors are refused or accepted as the file says`() {
        assertTrue(ROLE_URI_VECTORS.isNotEmpty())
        for ((uri, outcome) in ROLE_URI_VECTORS) {
            val expected =
                when (outcome) {
                    "missing" -> Outcome("CERT_ROLE_MISSING", null)
                    "invalid" -> Outcome("CERT_ROLE_INVALID", null)
                    "source" -> Outcome(null, principal())
                    else -> Outcome("CERT_IDENTITY_MISMATCH", null)
                }
            assertEquals(expected, callWith(ForgedRole.Uris(listOf(uri))), uri)
        }
    }

    private fun principal(): AgentPrincipal {
        val agent = PkiFixtures.AGENT
        return AgentPrincipal(agent.agentId, agent.tenantId, "a", AgentRole.SOURCE)
    }
}
