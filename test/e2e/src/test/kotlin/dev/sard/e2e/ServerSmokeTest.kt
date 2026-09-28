// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.e2e

import dev.sard.proto.agent.v1.EnrollRequest
import dev.sard.proto.agent.v1.EnrollmentServiceGrpcKt
import io.grpc.Status
import io.grpc.StatusException
import kotlinx.coroutines.runBlocking
import org.junit.jupiter.api.extension.RegisterExtension
import java.net.URI
import java.net.http.HttpClient
import java.net.http.HttpRequest
import java.net.http.HttpResponse
import java.nio.file.Files
import kotlin.io.path.createTempFile
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

/** The installation comes up: REST answers, the gRPC port speaks TLS with the Sard CA. */
class ServerSmokeTest {
    @Test
    fun `the server is healthy and reports the version it was built with`() {
        val response =
            HttpClient.newHttpClient().send(
                HttpRequest.newBuilder(URI.create("${sard.httpBase}/api/v1/status")).build(),
                HttpResponse.BodyHandlers.ofString(),
            )
        assertEquals(200, response.statusCode(), response.body())
        assertEquals(E2e.version, VERSION.find(response.body())?.groupValues?.get(1), response.body())
    }

    @Test
    fun `the gRPC port presents a certificate for the agent endpoint issued by the server CA`() {
        val chain = ServerTls.presentedChain(sard)
        val names = chain.first().subjectAlternativeNames.map { it[1] }
        assertTrue(SardEnvironment.SERVER_ALIAS in names, "SAN $names")
        assertEquals(2, chain.size, "leaf and root")
        assertEquals(ServerTls.fingerprint(caCertificate()), ServerTls.fingerprint(chain.last()))
    }

    @Test
    fun `a client trusting the server CA reaches gRPC over TLS`() {
        val channel = ServerTls.channel(sard, ServerTls.trusting(caPem()).build())
        try {
            val stub = EnrollmentServiceGrpcKt.EnrollmentServiceCoroutineStub(channel)
            val failure = runCatching { runBlocking { stub.enroll(EnrollRequest.getDefaultInstance()) } }.exceptionOrNull()
            // An empty request reaches Enroll, which rejects the missing token.
            assertEquals(Status.Code.INVALID_ARGUMENT, (failure as StatusException).status.code, failure.toString())
        } finally {
            channel.shutdownNow()
        }
    }

    /** The CA certificate from the server's own CA directory. */
    private fun caPem(): String {
        val file = createTempFile("ca", ".crt")
        try {
            sard.server.copyFileFromContainer(SardEnvironment.CA_CERT_PATH, file.toString())
            return Files.readString(file)
        } finally {
            Files.deleteIfExists(file)
        }
    }

    private fun caCertificate() = ServerTls.parse(caPem())

    companion object {
        @JvmField
        @RegisterExtension
        val sard = SardEnvironment()

        private val VERSION = Regex(""""version"\s*:\s*"([^"]*)"""")
    }
}
