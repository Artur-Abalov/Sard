// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server

import dev.sard.proto.agent.v1.EnrollRequest
import dev.sard.proto.agent.v1.EnrollmentServiceGrpcKt
import dev.sard.server.pki.AgentIdentity
import dev.sard.server.pki.CaFingerprint
import dev.sard.server.pki.CertificateAuthority
import dev.sard.server.pki.FileCertificateAuthority
import dev.sard.server.pki.Pem
import io.grpc.ChannelCredentials
import io.grpc.Grpc
import io.grpc.InsecureChannelCredentials
import io.grpc.Status
import io.grpc.StatusException
import io.grpc.TlsChannelCredentials
import kotlinx.coroutines.runBlocking
import org.bouncycastle.asn1.x500.X500Name
import org.bouncycastle.operator.jcajce.JcaContentSignerBuilder
import org.bouncycastle.pkcs.jcajce.JcaPKCS10CertificationRequestBuilder
import org.junit.jupiter.api.io.TempDir
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.grpc.test.autoconfigure.LocalGrpcServerPort
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.context.annotation.Import
import java.nio.file.Path
import java.security.KeyPairGenerator
import java.security.SecureRandom
import java.security.cert.X509Certificate
import java.security.spec.ECGenParameterSpec
import java.time.Clock
import java.util.UUID
import javax.net.ssl.SSLContext
import javax.net.ssl.SSLSocket
import javax.net.ssl.X509TrustManager
import kotlin.test.Test
import kotlin.test.assertEquals

/** ADR 0014: the gRPC port speaks TLS with a certificate from the Sard CA; client certificates are optional. */
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.RANDOM_PORT,
    properties = ["spring.grpc.server.port=0"],
)
@Import(TestcontainersConfiguration::class)
class GrpcTlsIntegrationTest(
    @Autowired private val ca: CertificateAuthority,
    @LocalGrpcServerPort private val grpcPort: Int,
) {
    @TempDir
    lateinit var tmp: Path

    private fun enroll(credentials: ChannelCredentials): Status.Code {
        val channel = Grpc.newChannelBuilderForAddress("localhost", grpcPort, credentials).build()
        try {
            val stub = EnrollmentServiceGrpcKt.EnrollmentServiceCoroutineStub(channel)
            val e = runCatching { runBlocking { stub.enroll(EnrollRequest.getDefaultInstance()) } }.exceptionOrNull()
            return (e as StatusException).status.code
        } finally {
            channel.shutdownNow()
        }
    }

    private fun trusting() = TlsChannelCredentials.newBuilder().trustManager(ca.caBundlePem().byteInputStream())

    /** A TLS client presenting a certificate [issuer] signed for a fresh P-256 key. */
    private fun withClientCertificate(issuer: CertificateAuthority): ChannelCredentials {
        val generator = KeyPairGenerator.getInstance("EC").apply { initialize(ECGenParameterSpec("secp256r1")) }
        val keys = generator.generateKeyPair()
        val signer = JcaContentSignerBuilder("SHA256withECDSA").build(keys.private)
        val csr = JcaPKCS10CertificationRequestBuilder(X500Name("CN=test"), keys.public).build(signer).encoded
        val issued = issuer.issueAgentCertificate(csr, AgentIdentity(UUID.randomUUID(), UUID.randomUUID()))
        return trusting()
            .keyManager(issued.chainPem.byteInputStream(), Pem.privateKey(keys.private).byteInputStream())
            .build()
    }

    @Test
    fun `a client trusting the Sard CA reaches the server without a client certificate`() {
        // An empty request reaches Enroll, which rejects its missing token.
        assertEquals(Status.Code.UNAUTHENTICATED, enroll(trusting().build()))
    }

    /** A2a: a new agent trusts no CA yet and pins the root it finds in the handshake. */
    @Test
    fun `the handshake presents the CA an enrollment token pins`() {
        val trustAll =
            object : X509TrustManager {
                override fun checkClientTrusted(
                    chain: Array<X509Certificate>,
                    authType: String,
                ) = Unit

                override fun checkServerTrusted(
                    chain: Array<X509Certificate>,
                    authType: String,
                ) = Unit

                override fun getAcceptedIssuers() = emptyArray<X509Certificate>()
            }
        val context = SSLContext.getInstance("TLS").apply { init(null, arrayOf(trustAll), null) }
        val chain =
            (context.socketFactory.createSocket("localhost", grpcPort) as SSLSocket).use { socket ->
                socket.sslParameters = socket.sslParameters.apply { applicationProtocols = arrayOf("h2") }
                socket.startHandshake()
                socket.session.peerCertificates.map { it as X509Certificate }
            }
        assertEquals(2, chain.size)
        assertEquals(ca.fingerprint(), CaFingerprint.of(chain.last()))
    }

    @Test
    fun `a client that does not trust the Sard CA fails the handshake`() {
        assertEquals(Status.Code.UNAVAILABLE, enroll(TlsChannelCredentials.create()))
    }

    @Test
    fun `plaintext is refused`() {
        assertEquals(Status.Code.UNAVAILABLE, enroll(InsecureChannelCredentials.create()))
    }

    @Test
    fun `a client certificate from the Sard CA is accepted`() {
        assertEquals(Status.Code.UNAUTHENTICATED, enroll(withClientCertificate(ca)))
    }

    @Test
    fun `a client certificate from another CA is refused at the handshake`() {
        val stranger =
            FileCertificateAuthority(tmp.resolve("pki"), listOf("localhost"), Clock.systemUTC(), SecureRandom())
        assertEquals(Status.Code.UNAVAILABLE, enroll(withClientCertificate(stranger)))
    }
}
