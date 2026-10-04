// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.e2e

import dev.sard.proto.agent.v1.AgentServiceGrpcKt
import dev.sard.proto.agent.v1.RenewCertificateRequest
import io.grpc.ChannelCredentials
import io.grpc.Grpc
import io.grpc.ManagedChannel
import io.grpc.Status
import io.grpc.StatusException
import io.grpc.TlsChannelCredentials
import kotlinx.coroutines.runBlocking
import java.io.ByteArrayInputStream
import java.security.MessageDigest
import java.security.cert.CertificateFactory
import java.security.cert.X509Certificate
import java.util.HexFormat
import javax.net.ssl.SSLContext
import javax.net.ssl.SSLSocket
import javax.net.ssl.X509TrustManager

/** TLS toward the gRPC port, seen from outside the way an agent sees it. */
internal object ServerTls {
    /**
     * The chain the server presents, leaf first, read without trusting anything:
     * a new agent pins the root it finds here (docs/specs/enrollment-token.md).
     */
    fun presentedChain(env: SardEnvironment): List<X509Certificate> {
        val context = SSLContext.getInstance("TLS").apply { init(null, arrayOf(TrustAll), null) }
        return (context.socketFactory.createSocket(env.grpcHost, env.grpcPort) as SSLSocket).use { socket ->
            socket.sslParameters =
                socket.sslParameters.apply {
                    applicationProtocols = arrayOf("h2")
                    serverNames = listOf(javax.net.ssl.SNIHostName(SardEnvironment.SERVER_ALIAS))
                }
            socket.startHandshake()
            socket.session.peerCertificates.map { it as X509Certificate }
        }
    }

    /** SHA-256 of the certificate's DER SubjectPublicKeyInfo, lowercase hex — the token's fingerprint. */
    fun fingerprint(certificate: X509Certificate): String =
        HexFormat.of().formatHex(MessageDigest.getInstance("SHA-256").digest(certificate.publicKey.encoded))

    fun parse(pem: String): X509Certificate =
        CertificateFactory.getInstance("X.509").generateCertificate(ByteArrayInputStream(pem.toByteArray())) as X509Certificate

    /** Credentials that trust only [caPem]; add a key manager for mTLS. */
    fun trusting(caPem: String): TlsChannelCredentials.Builder =
        TlsChannelCredentials.newBuilder().trustManager(caPem.byteInputStream())

    /** A channel to the gRPC port that checks the certificate against the agent endpoint's name. */
    fun channel(
        env: SardEnvironment,
        credentials: ChannelCredentials,
    ): ManagedChannel =
        Grpc
            .newChannelBuilderForAddress(env.grpcHost, env.grpcPort, credentials)
            .overrideAuthority(SardEnvironment.AGENT_ENDPOINT)
            .build()

    /**
     * The status AgentService.RenewCertificate answers with [credentials]. It is the one method
     * still unimplemented: UNIMPLEMENTED proves the call passed agent authentication (S3), while
     * a call the interceptor refuses is UNAUTHENTICATED.
     */
    fun renewCertificateStatus(
        env: SardEnvironment,
        credentials: ChannelCredentials,
    ): Status.Code {
        val channel = channel(env, credentials)
        try {
            val stub = AgentServiceGrpcKt.AgentServiceCoroutineStub(channel)
            val failure =
                runCatching { runBlocking { stub.renewCertificate(RenewCertificateRequest.getDefaultInstance()) } }
                    .exceptionOrNull()
            return (failure as StatusException).status.code
        } finally {
            channel.shutdownNow()
        }
    }

    private object TrustAll : X509TrustManager {
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
}
