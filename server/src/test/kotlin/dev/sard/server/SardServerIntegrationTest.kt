// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server

import dev.sard.proto.agent.v1.AgentServiceGrpcKt
import dev.sard.proto.agent.v1.EnrollRequest
import dev.sard.proto.agent.v1.EnrollmentServiceGrpcKt
import dev.sard.proto.agent.v1.RegisterRequest
import dev.sard.proto.agent.v1.RenewCertificateRequest
import dev.sard.server.extension.ExtensionRegistry
import dev.sard.server.persistence.Agent
import dev.sard.server.persistence.AgentRepository
import dev.sard.server.pki.CertificateAuthority
import io.grpc.Grpc
import io.grpc.ManagedChannel
import io.grpc.Status
import io.grpc.StatusException
import io.grpc.TlsChannelCredentials
import kotlinx.coroutines.runBlocking
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.grpc.test.autoconfigure.LocalGrpcServerPort
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.boot.test.web.server.LocalServerPort
import org.springframework.context.annotation.Import
import org.springframework.jdbc.core.JdbcTemplate
import tools.jackson.databind.JsonNode
import tools.jackson.databind.ObjectMapper
import java.net.URI
import java.net.http.HttpClient
import java.net.http.HttpRequest
import java.net.http.HttpResponse
import java.nio.file.Files
import java.nio.file.Path
import java.time.Instant
import java.time.temporal.ChronoUnit
import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

/**
 * The open core with zero enterprise extensions: the context starts against a real
 * PostgreSQL, Flyway applies the baseline, REST, Actuator, OpenAPI and gRPC answer.
 */
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.RANDOM_PORT,
    properties = ["spring.grpc.server.port=0"],
)
@Import(TestcontainersConfiguration::class)
class SardServerIntegrationTest(
    @Autowired private val mapper: ObjectMapper,
    @Autowired private val jdbc: JdbcTemplate,
    @Autowired private val extensions: ExtensionRegistry,
    @Autowired private val agents: AgentRepository,
    @Autowired private val ca: CertificateAuthority,
    @LocalServerPort private val httpPort: Int,
    @LocalGrpcServerPort private val grpcPort: Int,
) {
    private val http = HttpClient.newHttpClient()

    private fun get(path: String): Pair<Int, JsonNode> {
        val request = HttpRequest.newBuilder(URI.create("http://localhost:$httpPort$path")).build()
        val response = http.send(request, HttpResponse.BodyHandlers.ofString())
        return response.statusCode() to mapper.readTree(response.body())
    }

    @Test
    fun `status reports the version and no verified restore yet`() {
        val (code, body) = get("/api/v1/status")
        assertEquals(200, code)
        assertEquals("dev", body.path("version").asString())
        assertTrue(body.has("lastVerifiedRestoreAt"), "field must be present: $body")
        assertTrue(body.path("lastVerifiedRestoreAt").isNull, "must be null: $body")
    }

    @Test
    fun `actuator health is up`() {
        val (code, body) = get("/actuator/health")
        assertEquals(200, code)
        assertEquals("UP", body.path("status").asString())
    }

    @Test
    fun `flyway applied every migration`() {
        val sql = "select version from flyway_schema_history where success order by installed_rank"
        val versions =
            listOf("1", "2", "202609271200", "202609271600", "202609281200", "202609281400") +
                listOf("202609301200", "202609301800", "202610011200", "202610011300", "202610021200", "202610041200") +
                listOf("202610071200", "202610081200", "202610091200")
        assertEquals(versions, jdbc.queryForList(sql, String::class.java))
        assertEquals(0, jdbc.queryForObject("select count(*) from agents", Int::class.java))
    }

    /** The JPA mapping matches the Flyway schema (ddl-auto=validate plus a round trip). */
    @Test
    fun `an agent row round-trips through the repository`() {
        val now = Instant.now().truncatedTo(ChronoUnit.MICROS)
        val id = UUID.randomUUID()
        agents.save(Agent(id, "db1", "1.2.3", now, lastSeenAt = null))
        val loaded = agents.findById(id).orElseThrow()
        assertEquals(listOf<Any?>(id, "db1", "1.2.3"), listOf(loaded.id, loaded.hostname, loaded.agentVersion))
        assertEquals(now, loaded.registeredAt)
        assertEquals(null, loaded.lastSeenAt)
        agents.deleteById(id)
    }

    @Test
    fun `the open core runs without extensions`() {
        assertEquals(emptyList(), extensions.ids())
    }

    /** Also exports the OpenAPI document the web client is generated from (make openapi). */
    @Test
    fun `openapi describes the status endpoint`() {
        val (code, body) = get("/v3/api-docs")
        assertEquals(200, code)
        assertTrue(body.path("paths").has("/api/v1/status"), "paths: ${body.path("paths")}")
        val out = Path.of("build/openapi/openapi.json")
        Files.createDirectories(out.parent)
        Files.writeString(out, mapper.writerWithDefaultPrettyPrinter().writeValueAsString(body) + "\n")
    }

    /** TLS that trusts the Sard CA only; no client certificate. */
    private fun grpcStatus(call: suspend (ManagedChannel) -> Any): Status.Code {
        val credentials = TlsChannelCredentials.newBuilder().trustManager(ca.caBundlePem().byteInputStream()).build()
        val channel = Grpc.newChannelBuilderForAddress("localhost", grpcPort, credentials).build()
        try {
            val e = runCatching { runBlocking { call(channel) } }.exceptionOrNull()
            return (e as StatusException).status.code
        } finally {
            channel.shutdownNow()
        }
    }

    /** S3: without a client certificate AgentService is refused; AgentAuthIntegrationTest covers the rest. */
    @Test
    fun `agent service refuses a caller without a client certificate`() {
        val register = grpcStatus { agentStub(it).register(RegisterRequest.getDefaultInstance()) }
        val renew = grpcStatus { agentStub(it).renewCertificate(RenewCertificateRequest.getDefaultInstance()) }
        assertEquals(listOf(Status.Code.UNAUTHENTICATED, Status.Code.UNAUTHENTICATED), listOf(register, renew))
    }

    @Test
    fun `enrollment service rejects a request without a token`() {
        // TOKEN_MALFORMED (docs/specs/server/agent-enrollment.feature): the empty string is not a token.
        val enroll = grpcStatus { enrollmentStub(it).enroll(EnrollRequest.getDefaultInstance()) }
        assertEquals(Status.Code.INVALID_ARGUMENT, enroll)
    }

    private fun agentStub(channel: ManagedChannel) = AgentServiceGrpcKt.AgentServiceCoroutineStub(channel)

    private fun enrollmentStub(channel: ManagedChannel): EnrollmentServiceGrpcKt.EnrollmentServiceCoroutineStub =
        EnrollmentServiceGrpcKt.EnrollmentServiceCoroutineStub(channel)
}
