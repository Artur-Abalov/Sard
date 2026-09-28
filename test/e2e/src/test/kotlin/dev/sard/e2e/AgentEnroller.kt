// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.e2e

import dev.sard.proto.agent.v1.EnrollRequest
import dev.sard.proto.agent.v1.EnrollmentServiceGrpcKt
import io.grpc.ChannelCredentials
import kotlinx.coroutines.runBlocking
import org.bouncycastle.asn1.x500.X500Name
import org.bouncycastle.operator.jcajce.JcaContentSignerBuilder
import org.bouncycastle.pkcs.jcajce.JcaPKCS10CertificationRequestBuilder
import java.security.KeyPairGenerator
import java.security.spec.ECGenParameterSpec
import java.util.Base64

/** What enrollment leaves on an agent host: its identity, key, certificate chain and the CA to trust. */
internal class AgentCredentials(
    val agentId: String,
    val keyPem: String,
    val chainPem: String,
    val caPem: String,
) {
    /** mTLS credentials presenting this agent's certificate and trusting only its CA. */
    fun channelCredentials(): ChannelCredentials =
        ServerTls.trusting(caPem).keyManager(chainPem.byteInputStream(), keyPem.byteInputStream()).build()

    override fun toString() = "AgentCredentials(agentId=$agentId)"
}

/**
 * Enrolls an agent with a token.
 *
 * REPLACEMENT POINT (A2): [enroll] is the only place tests enroll. Until
 * `sard-agent enroll` is in main it is a minimal agent-side Enroll client:
 * P-256 key and CSR, the server's root pinned against the token's
 * fingerprint before anything is sent, the returned CA checked again. Once A2
 * is in main, replace its body with `sard-agent enroll --server
 * sard-server:9090 --token <token>` run in an agent container on the
 * environment's network, and read the key, chain and CA back from the files
 * it writes; the callers stay as they are.
 */
internal object AgentEnroller {
    fun enroll(
        env: SardEnvironment,
        token: String,
        hostname: String = "e2e-agent",
    ): AgentCredentials {
        val pinned = token.substringAfterLast('.')
        val root = ServerTls.presentedChain(env).last()
        check(ServerTls.fingerprint(root) == pinned) { "the server presents a CA other than the token's" }

        val keys = KeyPairGenerator.getInstance("EC").apply { initialize(ECGenParameterSpec("secp256r1")) }.generateKeyPair()
        val signer = JcaContentSignerBuilder("SHA256withECDSA").build(keys.private)
        val csr = JcaPKCS10CertificationRequestBuilder(X500Name("CN=$hostname"), keys.public).build(signer).encoded
        val keyPem = pem("PRIVATE KEY", keys.private.encoded).also(env::secret)

        val channel = ServerTls.channel(env, ServerTls.trusting(pem("CERTIFICATE", root.encoded)).build())
        val response =
            try {
                val request =
                    EnrollRequest
                        .newBuilder()
                        .setEnrollmentToken(token)
                        .setCsrDer(com.google.protobuf.ByteString.copyFrom(csr))
                        .setHostname(hostname)
                        .build()
                runBlocking { EnrollmentServiceGrpcKt.EnrollmentServiceCoroutineStub(channel).enroll(request) }
            } finally {
                channel.shutdownNow()
            }
        check(ServerTls.fingerprint(ServerTls.parse(response.caBundlePem)) == pinned) {
            "Enroll returned a CA other than the token's"
        }
        return AgentCredentials(response.agentId, keyPem, response.certificateChainPem, response.caBundlePem)
    }

    private fun pem(
        type: String,
        der: ByteArray,
    ): String {
        val body = Base64.getMimeEncoder(64, "\n".toByteArray()).encodeToString(der)
        return "-----BEGIN $type-----\n$body\n-----END $type-----\n"
    }
}
