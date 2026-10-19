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
import dev.sard.server.selfagent.captureEvents
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
import org.bouncycastle.asn1.DERNull
import org.bouncycastle.asn1.x500.X500Name
import org.bouncycastle.asn1.x509.Extension
import org.bouncycastle.asn1.x509.SubjectPublicKeyInfo
import org.bouncycastle.cert.X509v3CertificateBuilder
import org.bouncycastle.cert.jcajce.JcaX509CertificateConverter
import org.bouncycastle.operator.jcajce.JcaContentSignerBuilder
import java.lang.reflect.Proxy
import java.math.BigInteger
import java.security.KeyPairGenerator
import java.security.cert.Certificate
import java.security.cert.X509Certificate
import java.security.spec.ECGenParameterSpec
import java.time.Clock
import java.util.Date
import javax.net.ssl.SSLSession
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNotEquals
import kotlin.test.assertNotNull
import kotlin.test.assertNull
import kotlin.test.assertTrue

private class ClosedCall(
    private val method: MethodDescriptor<*, *>,
    private val peer: X509Certificate? = anyCertificate(),
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
            .apply { peer?.let { set(Grpc.TRANSPORT_ATTR_SSL_SESSION, session(it)) } }
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

/** A certificate whose SAN extension is not a SAN: reading its names fails with a parsing error. */
private fun unparseableCertificate(): X509Certificate {
    val keys =
        KeyPairGenerator
            .getInstance("EC")
            .apply { initialize(ECGenParameterSpec("secp256r1")) }
            .generateKeyPair()
    val name = X500Name("CN=broken")
    val builder =
        X509v3CertificateBuilder(
            name,
            BigInteger.TEN,
            Date.from(PkiFixtures.NOW),
            Date.from(PkiFixtures.NOW.plusSeconds(DAY)),
            name,
            SubjectPublicKeyInfo.getInstance(keys.public.encoded),
        ).addExtension(Extension.subjectAlternativeName, false, DERNull.INSTANCE)
    val signer = JcaContentSignerBuilder(Keys.SIGNATURE).build(keys.private)
    return JcaX509CertificateConverter().getCertificate(builder.build(signer))
}

private const val HEX = 16
private const val DAY = 86_400L

/** A8d-3 (ADR 0009 amendment): an unreachable database is a retryable server fault, never a refusal. */
@MutFlowTest
class AgentAuthUnavailableTest {
    private val broken = CertificateStandings { error("connection to db-secret-host refused") }
    private val method = AgentServiceGrpc.getProbeMethod()

    private fun intercept(
        call: ClosedCall,
        standings: CertificateStandings = broken,
    ): Boolean {
        var reached = false
        val next =
            ServerCallHandler<Any, Any> { _, _ ->
                reached = true
                object : ServerCall.Listener<Any>() {}
            }
        val interceptor = AgentAuthInterceptor(AgentAuthenticator(standings, Clock.systemUTC()), emptySet())
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

    /** The verdict on a certificate is the caller's fault, not the server's: it is refused, never retried. */
    @Test
    fun `a certificate that cannot be parsed is not answered UNAVAILABLE`() {
        val call = ClosedCall(method, peer = unparseableCertificate())
        assertFalse(intercept(call, CertificateStandings { standing }))
        val status = assertNotNull(call.status)
        assertNotEquals(Status.Code.UNAVAILABLE, status.code)
        val info =
            StatusProto
                .fromStatusAndTrailers(status, call.trailers)
                .detailsList
                .single()
                .unpack(ErrorInfo::class.java)
        assertEquals("CERT_MISSING", info.reason)
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

    private fun callWith(
        role: ForgedRole,
        recorded: CertificateStanding = standing,
        on: MethodDescriptor<*, *> = method,
    ): Outcome = attempt(role, true, recorded, on).first

    /** The outcome of a call and what the interceptor logged; [recorded]: does the database know the certificate. */
    private fun attempt(
        role: ForgedRole,
        recorded: Boolean,
        record: CertificateStanding = standing,
        on: MethodDescriptor<*, *> = method,
    ): Pair<Outcome, List<String>> {
        val certificate = ForgedCertificates(ca).forge(agentKeys, serial, PkiFixtures.AGENT, role, PkiFixtures.NOW)
        val call = ClosedCall(on, certificate)
        var principal: AgentPrincipal? = null
        val next =
            ServerCallHandler<Any, Any> { _, _ ->
                principal = AgentPrincipal.KEY.get()
                object : ServerCall.Listener<Any>() {}
            }
        val standings = CertificateStandings { if (recorded && it == serial.toString(HEX)) record else null }
        val interceptor = AgentAuthInterceptor(AgentAuthenticator(standings, PkiFixtures.CLOCK), emptySet())
        val logged = captureEvents { MutFlow.underTest { interceptor.interceptCall(call, Metadata(), next) } }
        val reason =
            call.status?.let {
                StatusProto
                    .fromStatusAndTrailers(it, call.trailers)
                    .detailsList
                    .single()
                    .unpack(ErrorInfo::class.java)
                    .reason
            }
        return Outcome(reason, principal) to logged.map { it.text }
    }

    private val source = ForgedRole.Text("source")
    private val confirm = AgentServiceGrpc.getConfirmCertificateMethod()

    @Test
    fun `a superseded certificate is refused CERT_SUPERSEDED on every call including the confirmation`() {
        val superseded = standing.copy(supersededAt = PkiFixtures.NOW.minusSeconds(DAY))
        assertEquals(Outcome("CERT_SUPERSEDED", null), callWith(source, superseded))
        assertEquals(Outcome("CERT_SUPERSEDED", null), callWith(source, superseded, confirm))
    }

    @Test
    fun `an unconfirmed certificate is refused CERT_NOT_CONFIRMED except on the confirmation`() {
        val unconfirmed = standing.copy(unconfirmed = true)
        assertEquals(Outcome("CERT_NOT_CONFIRMED", null), callWith(source, unconfirmed))
        assertNull(callWith(source, unconfirmed, confirm).reason)
        assertNull(callWith(source, standing, confirm).reason)
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

    private fun refusalLog(
        role: ForgedRole,
        recorded: Boolean = true,
        record: CertificateStanding = standing,
    ) = attempt(role, recorded, record).second.single()

    private val head = "agent call refused: reason="
    private val who = "serial=${serial.toString(HEX)} agent=${PkiFixtures.AGENT.agentId}"
    private val tenant = "tenant=${PkiFixtures.AGENT.tenantId}"
    private val where = "method=${method.fullMethodName}"

    @Test
    fun `an unknown certificate is logged with its tenant and the missing-record hint`() {
        val line = refusalLog(ForgedRole.Text("source"), recorded = false)
        val hint =
            "the certificate was issued by this server's CA, but the database has no record of it: " +
                "the database was not restored or was restored from a copy older than the agent's enrollment"
        assertEquals("${head}CERT_UNKNOWN $who $tenant $where: $hint", line)
    }

    @Test
    fun `a certificate without a role URI is logged with its tenant and the re-enroll hint`() {
        assertEquals("${head}CERT_ROLE_MISSING $who $tenant $where: $ROLE_HINT_TEXT", refusalLog(ForgedRole.Absent))
    }

    @Test
    fun `a certificate with several role URIs is logged with its tenant and the re-enroll hint`() {
        val twice = ForgedRole.Uris(listOf(AgentRoleUri.of(AgentRole.SOURCE), AgentRoleUri.of(AgentRole.SOURCE)))
        assertEquals("${head}CERT_ROLE_INVALID $who $tenant $where: $ROLE_HINT_TEXT", refusalLog(twice))
    }

    @Test
    fun `a call without a certificate is logged without tenant and without a hint`() {
        val standings = CertificateStandings { standing }
        val interceptor = AgentAuthInterceptor(AgentAuthenticator(standings, PkiFixtures.CLOCK), emptySet())
        val next = ServerCallHandler<Any, Any> { _, _ -> object : ServerCall.Listener<Any>() {} }
        val call = ClosedCall(method, peer = null)
        val logged = captureEvents { MutFlow.underTest { interceptor.interceptCall(call, Metadata(), next) } }
        assertEquals("${head}CERT_MISSING serial=null agent=null $where", logged.single().text)
    }

    @Test
    fun `an identity mismatch is logged without tenant and without a hint`() {
        assertEquals("${head}CERT_IDENTITY_MISMATCH $who $where", refusalLog(ForgedRole.Text("verifier")))
    }

    @Test
    fun `a revoked, superseded or unconfirmed certificate is logged without tenant and without a hint`() {
        val revoked = standing.copy(revokedAt = PkiFixtures.NOW)
        val superseded = standing.copy(supersededAt = PkiFixtures.NOW.minusSeconds(DAY))
        val unconfirmed = standing.copy(unconfirmed = true)
        assertEquals("${head}CERT_REVOKED $who $where", refusalLog(source, record = revoked))
        assertEquals("${head}CERT_SUPERSEDED $who $where", refusalLog(source, record = superseded))
        assertEquals("${head}CERT_NOT_CONFIRMED $who $where", refusalLog(source, record = unconfirmed))
    }
}

private const val ROLE_HINT_TEXT =
    "the certificate carries no valid role of the agent (it was issued before roles existed, or is not ours): " +
        "re-enroll the agent"
