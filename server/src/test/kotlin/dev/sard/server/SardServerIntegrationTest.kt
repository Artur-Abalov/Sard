// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server

import dev.sard.proto.agent.v1.AgentServiceGrpcKt
import dev.sard.proto.agent.v1.RegisterRequest
import dev.sard.server.extension.ExtensionRegistry
import dev.sard.server.persistence.Agent
import dev.sard.server.persistence.AgentRepository
import io.grpc.ManagedChannelBuilder
import io.grpc.Status
import io.grpc.StatusException
import kotlinx.coroutines.runBlocking
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.grpc.test.autoconfigure.LocalGrpcServerPort
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.boot.test.web.server.LocalServerPort
import org.springframework.context.annotation.Import
import org.springframework.jdbc.core.JdbcTemplate
import tools.jackson.databind.JsonNode
import tools.jackson.databind.ObjectMapper
import tools.jackson.databind.node.ObjectNode
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
    fun `flyway applied the agents baseline`() {
        val version = jdbc.queryForObject("select version from flyway_schema_history where success", String::class.java)
        assertEquals("1", version)
        assertEquals(0, jdbc.queryForObject("select count(*) from agents", Int::class.java))
    }

    /** The JPA mapping matches the Flyway schema (ddl-auto=validate plus a round trip). */
    @Test
    fun `an agent row round-trips through the repository`() {
        val now = Instant.now().truncatedTo(ChronoUnit.MICROS)
        val id = UUID.randomUUID()
        agents.save(Agent(id, "db1", "1.2.3", now, lastSeenAt = null))
        val loaded = agents.findById(id).orElseThrow()
        assertEquals(listOf<Any>(id, "db1", "1.2.3"), listOf(loaded.id, loaded.hostname, loaded.agentVersion))
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
        (body as ObjectNode).remove("servers") // random test port; not part of the contract
        val out = Path.of("build/openapi/openapi.json")
        Files.createDirectories(out.parent)
        Files.writeString(out, mapper.writerWithDefaultPrettyPrinter().writeValueAsString(body) + "\n")
    }

    @Test
    fun `agent service answers UNIMPLEMENTED`() {
        val channel = ManagedChannelBuilder.forAddress("localhost", grpcPort).usePlaintext().build()
        try {
            val stub = AgentServiceGrpcKt.AgentServiceCoroutineStub(channel)
            val e =
                runCatching { runBlocking { stub.register(RegisterRequest.newBuilder().setHostname("db1").build()) } }
                    .exceptionOrNull()
            assertEquals(Status.Code.UNIMPLEMENTED, (e as StatusException).status.code)
        } finally {
            channel.shutdownNow()
        }
    }
}
