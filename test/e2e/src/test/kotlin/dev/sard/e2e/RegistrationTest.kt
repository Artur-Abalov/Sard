// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.e2e

import dev.sard.proto.agent.v1.AgentServiceGrpcKt
import dev.sard.proto.agent.v1.RenewCertificateRequest
import io.grpc.ChannelCredentials
import io.grpc.Status
import io.grpc.StatusException
import kotlinx.coroutines.runBlocking
import org.junit.jupiter.api.extension.RegisterExtension
import kotlin.test.Test
import kotlin.test.assertEquals

/**
 * Token → Enroll → the issued certificate is accepted by the agent
 * authentication interceptor (S3). RenewCertificate is the one AgentService
 * method still unimplemented: reaching its UNIMPLEMENTED proves the call passed
 * authentication; without a certificate the same call is UNAUTHENTICATED.
 */
class RegistrationTest {
    @Test
    fun `a certificate issued by Enroll passes agent authentication`() {
        val agent = AgentEnroller.enroll(sard, EnrollmentTokens.create(sard))
        assertEquals(Status.Code.UNIMPLEMENTED, renewCertificate(agent.channelCredentials()))
    }

    @Test
    fun `without a client certificate AgentService is UNAUTHENTICATED`() {
        val caPem = AgentEnroller.enroll(sard, EnrollmentTokens.create(sard)).caPem
        assertEquals(Status.Code.UNAUTHENTICATED, renewCertificate(ServerTls.trusting(caPem).build()))
    }

    @Test
    fun `a token enrolls one agent only`() {
        val token = EnrollmentTokens.create(sard)
        AgentEnroller.enroll(sard, token)
        val second = runCatching { AgentEnroller.enroll(sard, token) }.exceptionOrNull()
        assertEquals(Status.Code.UNAUTHENTICATED, (second as StatusException).status.code, second.toString())
    }

    private fun renewCertificate(credentials: ChannelCredentials): Status.Code {
        val channel = ServerTls.channel(sard, credentials)
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

    companion object {
        @JvmField
        @RegisterExtension
        val sard = SardEnvironment()
    }
}
