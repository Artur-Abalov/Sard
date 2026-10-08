// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import dev.sard.server.TestcontainersConfiguration
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.boot.test.web.server.LocalServerPort
import org.springframework.context.annotation.Import
import tools.jackson.databind.JsonNode
import tools.jackson.databind.ObjectMapper
import java.net.URI
import java.net.http.HttpClient
import java.net.http.HttpRequest
import java.net.http.HttpResponse
import java.time.Instant
import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

/**
 * The spec is derived by swagger through Jackson 2, the server writes JSON with
 * Jackson 3: every DTO, serialized by the application's mapper, must match its
 * schema in /v3/api-docs — property names, required, null, enums and formats.
 */
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.RANDOM_PORT,
    properties = ["spring.grpc.server.port=0"],
)
@Import(TestcontainersConfiguration::class)
class ApiSerializationIntegrationTest(
    @Autowired private val mapper: ObjectMapper,
    @LocalServerPort private val port: Int,
) {
    private val spec: JsonNode by lazy {
        val request = HttpRequest.newBuilder(URI.create("http://localhost:$port/v3/api-docs")).build()
        mapper.readTree(HttpClient.newHttpClient().send(request, HttpResponse.BodyHandlers.ofString()).body())
    }

    private val id = UUID.fromString("0192f7a0-0000-7000-8000-000000000001")
    private val at = Instant.parse("2026-09-27T10:00:00Z")
    private val schema = mapOf("type" to "object", "properties" to mapOf("paths" to mapOf("type" to "array")))

    private val summary =
        AgentSummary(
            id,
            "db1",
            AgentStatus.ONLINE,
            "0.1.0",
            "linux",
            "amd64",
            at,
            at,
            revokedAt = null,
            duplicateSessionAt = at,
            outdated = false,
            builtin = true,
        )
    private val step =
        RunStep(
            id,
            1,
            StepAction.BACKUP,
            StepStatus.SUCCEEDED,
            StepPhase.UPLOADING,
            id,
            id,
            "files",
            "local",
            10,
            10,
            filesProcessed = 3,
            filesTotal = 4,
            message = null,
            backup = BackupOutput("4f1c", 10, 2, "a1b2", partial = false),
            at,
            at,
            at,
            at,
        )
    private val queuedStep =
        RunStep(
            id,
            1,
            StepAction.RUN,
            StepStatus.QUEUED,
            phase = null,
            id,
            sourceId = null,
            "files",
            repositoryName = null,
            bytesProcessed = null,
            bytesTotal = null,
            filesProcessed = null,
            filesTotal = null,
            message = null,
            backup = null,
            queuedAt = at,
            dispatchedAt = null,
            startedAt = null,
            finishedAt = null,
        )
    private val source = Source(id, "etc", id, "files", "local", mapOf("paths" to listOf("/etc")), at, at)
    private val token =
        EnrollmentToken(id, EnrollmentTokenStatus.USED, at, at, at, revokedAt = null, agentId = id, label = null)
    private val snapshot =
        Snapshot(id, "4f1c", id, id, id, id, "local", "a1b2", 10, 2, at, forgottenAt = null, partial = false)
    private val summaryRun =
        RunSummary(id, id, "etc", false, id, RunTrigger.MANUAL, RunStatus.FAILED, "boom", at, at, at)

    private val samples: List<Any> =
        listOf(
            StatusResponse("1.0.0", lastVerifiedRestoreAt = null),
            Overview(1, 3, FirstSteps(true, true, false, false, false, complete = false)),
            SessionRequest("secret"),
            Session(id, at),
            AgentPage(listOf(summary), nextCursor = "c2"),
            AgentDetails(
                id,
                "db1",
                AgentStatus.OFFLINE,
                agentVersion = null,
                os = null,
                arch = null,
                at,
                lastSeenAt = null,
                revokedAt = at,
                duplicateSessionAt = null,
                protocolVersion = 1,
                plugins = listOf(AgentPlugin("files", "0.1.0", listOf(StepAction.BACKUP), schema)),
                repositories = listOf(AgentRepository("local", "local", repositoryId = null, "file")),
                secretNames = listOf("pg"),
                scriptNames = listOf("flush"),
                outdated = true,
                builtin = false,
            ),
            AgentInstall(
                downloadsEnabled = true,
                agentVersion = "v1.4.0",
                resticVersion = null,
                InstallArch.ARM64,
                InstallFormat.TAR,
                signed = false,
                ReleaseKey("DF5D5B6DB257DBFA", "RWT621eybVtd38CL7B33xZrcc8ArYiPt3GlKXyJuk9ZQzDnoUV+kLi2d"),
                manualInstallDoc = "https://example.com/doc",
                steps = listOf(InstallStep(StepKind.REPO_INIT, listOf("true"), optional = false)),
            ),
            AgentUpgrade(
                downloadsEnabled = true,
                agentVersion = "v1.4.0",
                resticVersion = "0.19.1",
                arch = null,
                InstallFormat.DEB,
                signed = true,
                ReleaseKey("DF5D5B6DB257DBFA", "RWT621eybVtd38CL7B33xZrcc8ArYiPt3GlKXyJuk9ZQzDnoUV+kLi2d"),
                manualInstallDoc = "https://example.com/doc",
                steps = emptyList(),
                reason = UpgradeReason.ARCH_UNKNOWN,
                keepsConfiguration = false,
            ),
            CreateEnrollmentTokenRequest(ttlSeconds = 3600),
            CreatedEnrollmentToken(id, "sard_x.y", "sard-agent enroll --server s:9090 --token sard_x.y", at, false),
            EnrollmentTokenPage(listOf(token), nextCursor = null),
            SourceInput("etc", id, "files", "local", mapOf("paths" to listOf("/etc"))),
            source,
            SourcePage(listOf(source), nextCursor = null),
            SnapshotPage(listOf(snapshot), nextCursor = null),
            Run(
                id,
                id,
                "etc",
                false,
                id,
                RunTrigger.MANUAL,
                RunStatus.RUNNING,
                null,
                at,
                at,
                finishedAt = null,
                listOf(step, queuedStep),
            ),
            RunPage(listOf(summaryRun), nextCursor = null),
            LogPage(
                listOf(LogLine(1, null, LogLevel.INFO, "started")),
                nextAfterSeq = 1,
                hasMore = false,
                truncated = false,
            ),
            Problem("about:blank", "Not Found", 404, detail = null, ErrorCode.NOT_FOUND),
            ValidationProblem(
                "about:blank",
                "Unprocessable",
                422,
                "no such repository",
                ErrorCode.UNKNOWN_REPOSITORY,
                listOf(FieldError("repositoryName", "not in the agent's last Register")),
            ),
            RunActiveProblem("about:blank", "Conflict", 409, detail = null, ErrorCode.RUN_ACTIVE, id),
            TokenConflictProblem("about:blank", "Conflict", 409, null, ErrorCode.TOKEN_EXPIRED, agentId = null),
        )

    @Test
    fun `every DTO serializes as its schema says`() {
        for (sample in samples) {
            val name = sample.javaClass.simpleName
            check(mapper.valueToTree(sample), ref("#/components/schemas/$name"), name)
        }
    }

    @Test
    fun `every schema has a sample here`() {
        val objects =
            spec
                .path("components")
                .path("schemas")
                .properties()
                .filter { it.value.has("properties") }
        assertEquals(objects.map { it.key }.toSet() - NESTED, samples.map { it.javaClass.simpleName }.toSet())
    }

    private fun ref(pointer: String): JsonNode = spec.at(pointer.removePrefix("#"))

    private fun JsonNode.strings(): List<String> = iterator().asSequence().map { it.asString() }.toList()

    private fun types(schema: JsonNode): List<String> =
        if (schema.path("type").isArray) schema.path("type").strings() else listOf(schema.path("type").asString())

    private fun check(
        node: JsonNode,
        schemaNode: JsonNode,
        where: String,
    ) {
        val schema = if (schemaNode.has("\$ref")) ref(schemaNode.path("\$ref").asString()) else schemaNode
        val alternatives = schema.path("oneOf")
        when {
            alternatives.isArray -> checkOneOf(node, alternatives, where)
            node.isNull -> assertTrue("null" in types(schema), "$where is null: $schema")
            else -> checkValue(node, schema, where)
        }
    }

    /** springdoc writes a nullable reference as oneOf [ref, null]. */
    private fun checkOneOf(
        node: JsonNode,
        alternatives: JsonNode,
        where: String,
    ) {
        val nullable = alternatives.any { it.path("type").asString() == "null" }
        if (node.isNull) {
            assertTrue(nullable, "$where is null")
        } else {
            check(node, alternatives.first { it.path("type").asString() != "null" }, where)
        }
    }

    private fun checkValue(
        node: JsonNode,
        schema: JsonNode,
        where: String,
    ) {
        val enum = schema.path("enum")
        if (enum.isArray) assertTrue(enum.any { it == node }, "$where = $node: $enum")
        when (val type = types(schema).first { it != "null" }) {
            "object" -> checkObject(node, schema, where)
            "array" -> node.forEachIndexed { i, item -> check(item, schema.path("items"), "$where[$i]") }
            "string" -> checkString(node, schema.path("format").asString(), where)
            "integer" -> assertTrue(node.isIntegralNumber, "$where = $node")
            "boolean" -> assertTrue(node.isBoolean, "$where = $node")
            else -> error("$where: unexpected type $type")
        }
    }

    private fun checkObject(
        node: JsonNode,
        schema: JsonNode,
        where: String,
    ) {
        assertTrue(node.isObject, "$where = $node")
        val properties = schema.path("properties")
        if (properties.isMissingNode) return // a free-form map: config, configSchema
        assertEquals(properties.propertyNames().toSet(), node.propertyNames().toSet(), where)
        val required = schema.path("required").strings()
        assertTrue(node.propertyNames().containsAll(required), "$where lacks some of $required")
        properties.properties().forEach { (name, property) -> check(node.path(name), property, "$where.$name") }
    }

    private fun checkString(
        node: JsonNode,
        format: String,
        where: String,
    ) {
        assertTrue(node.isString, "$where = $node")
        when (format) {
            "uuid" -> UUID.fromString(node.asString())
            "date-time" -> Instant.parse(node.asString())
        }
    }

    private companion object {
        /** Reached through their parents' samples. */
        val NESTED =
            setOf(
                "AgentSummary",
                "InstallStep",
                "ReleaseKey",
                "FirstSteps",
                "AgentPlugin",
                "AgentRepository",
                "EnrollmentToken",
                "Snapshot",
                "RunStep",
                "BackupOutput",
                "RunSummary",
                "LogLine",
                "FieldError",
            )
    }
}
