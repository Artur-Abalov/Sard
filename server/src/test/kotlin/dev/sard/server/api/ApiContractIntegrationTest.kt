// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import com.google.protobuf.ProtocolMessageEnum
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
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue
import dev.sard.proto.agent.v1.Action as ProtoAction
import dev.sard.proto.agent.v1.LogLevel as ProtoLogLevel
import dev.sard.proto.agent.v1.StepPhase as ProtoStepPhase
import dev.sard.proto.agent.v1.StepStatus as ProtoStepStatus

/**
 * The REST contract of stage 1 as the web client sees it (/v3/api-docs): enums
 * agree with the agent protocol, the session guards every endpoint but sign-in
 * and status, and the stubs answer 501 until S8b implements them.
 */
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.RANDOM_PORT,
    properties = ["spring.grpc.server.port=0"],
)
@Import(TestcontainersConfiguration::class)
class ApiContractIntegrationTest(
    @Autowired private val mapper: ObjectMapper,
    @LocalServerPort private val port: Int,
) {
    private val http = HttpClient.newHttpClient()

    private val spec: JsonNode by lazy { mapper.readTree(send("GET", "/v3/api-docs").body()) }

    private fun send(
        method: String,
        path: String,
        body: String? = null,
        cookie: String? = null,
    ): HttpResponse<String> {
        val publisher = body?.let { HttpRequest.BodyPublishers.ofString(it) } ?: HttpRequest.BodyPublishers.noBody()
        val builder =
            HttpRequest
                .newBuilder(URI.create("http://localhost:$port$path"))
                .header("Content-Type", "application/json")
                .method(method, publisher)
        cookie?.let { builder.header("Cookie", "$SESSION_COOKIE=$it") }
        return http.send(builder.build(), HttpResponse.BodyHandlers.ofString())
    }

    /** A fresh session cookie value, signed in with the test admin password (build.gradle.kts). */
    private fun signIn(): String {
        val response = send("POST", "/api/v1/session", """{"password":"test-admin-password-2026"}""")
        val setCookie = response.headers().firstValue("Set-Cookie").orElseThrow()
        return Regex("$SESSION_COOKIE=([^;]+)").find(setCookie)!!.groupValues[1]
    }

    private fun schema(name: String): JsonNode = spec.path("components").path("schemas").path(name)

    private fun JsonNode.strings(): List<String> = iterator().asSequence().map { it.asString() }.toList()

    private fun enumValues(name: String): List<String> = schema(name).path("enum").strings()

    /** STEP_PHASE_DUMPING -> dumping; the zero value and protobuf's UNRECOGNIZED are not wire values. */
    private fun <E> protoValues(values: Array<E>): List<String> where E : Enum<E>, E : ProtocolMessageEnum {
        val prefix = values.first().name.removeSuffix("UNSPECIFIED")
        return values
            .filter { it.name != "UNRECOGNIZED" && !it.name.endsWith("_UNSPECIFIED") }
            .map { it.name.removePrefix(prefix).lowercase() }
    }

    @Test
    fun `step phase, log level and action are the proto values`() {
        assertEquals(protoValues(ProtoStepPhase.entries.toTypedArray()), enumValues("StepPhase"))
        assertEquals(protoValues(ProtoLogLevel.entries.toTypedArray()), enumValues("LogLevel"))
        assertEquals(protoValues(ProtoAction.entries.toTypedArray()), enumValues("StepAction"))
    }

    /** The agent reports only final states; the server adds its own around them (ADR 0013, run_steps). */
    @Test
    fun `step status is the proto final states plus the server's own`() {
        val final = protoValues(ProtoStepStatus.entries.toTypedArray())
        assertEquals(listOf("queued", "dispatched", "running") + final + "lost", enumValues("StepStatus"))
    }

    @Test
    fun `run, trigger, agent and token states`() {
        val run = listOf("queued", "dispatched", "running", "succeeded", "failed", "cancelled")
        assertEquals(run, enumValues("RunStatus"))
        assertEquals(listOf("schedule", "manual", "verification"), enumValues("RunTrigger"))
        assertEquals(listOf("online", "offline"), enumValues("AgentStatus"))
        assertEquals(listOf("active", "used", "expired", "revoked"), enumValues("EnrollmentTokenStatus"))
    }

    @Test
    fun `enums go over the wire as their schema values`() {
        val all =
            listOf(
                StepPhase.entries,
                LogLevel.entries,
                StepAction.entries,
                StepStatus.entries,
                RunStatus.entries,
                RunTrigger.entries,
                AgentStatus.entries,
                EnrollmentTokenStatus.entries,
                ErrorCode.entries,
            )
        for (entries in all) {
            val name = entries.first().javaClass.simpleName
            val wire = entries.map { mapper.readTree(mapper.writeValueAsString(it)).asString() }
            assertEquals(enumValues(name), wire, name)
            assertEquals(entries.map { it.name.lowercase() }, wire, name)
        }
    }

    private fun operations(): List<Pair<String, JsonNode>> =
        spec.path("paths").properties().flatMap { (path, item) ->
            item.properties().map { (method, op) -> "${method.uppercase()} $path" to op }
        }

    @Test
    fun `every operation but sign-in and status requires the session and describes 401`() {
        val public = setOf("POST /api/v1/session", "GET /api/v1/status")
        for ((name, op) in operations()) {
            if (name in public) {
                assertTrue(op.path("security").isArray && op.path("security").isEmpty, "$name: ${op.path("security")}")
                // Sign-in has its own 401, a wrong password.
                assertTrue(
                    op
                        .path("responses")
                        .path("401")
                        .path("description")
                        .asString() != NO_SESSION,
                    name,
                )
            } else {
                assertTrue(op.path("security").isMissingNode, "$name inherits the root security: $op")
                val unauthorized =
                    op
                        .path("responses")
                        .path("401")
                        .path("content")
                        .path(PROBLEM_JSON)
                assertEquals("#/components/schemas/Problem", unauthorized.path("schema").path("\$ref").asString(), name)
                assertEquals(
                    NO_SESSION,
                    op
                        .path("responses")
                        .path("401")
                        .path("description")
                        .asString(),
                    name,
                )
            }
        }
        val scheme = spec.path("components").path("securitySchemes").path("session")
        val cookie = listOf("type", "in", "name").map { scheme.path(it).asString() }
        assertEquals(listOf("apiKey", "cookie", "sard_session"), cookie)
        assertEquals(
            listOf("session"),
            spec
                .path("security")
                .iterator()
                .asSequence()
                .flatMap { it.propertyNames() }
                .toList(),
        )
    }

    /** springdoc drops the implicit 200 once a method declares any @ApiResponse. */
    @Test
    fun `every operation describes its success response`() {
        for ((name, op) in operations()) {
            val codes = op.path("responses").propertyNames()
            assertTrue(codes.any { it.startsWith("2") }, "$name: $codes")
        }
    }

    @Test
    fun `the server is the page's own origin`() {
        assertEquals(
            listOf("/"),
            spec
                .path("servers")
                .iterator()
                .asSequence()
                .map { it.path("url").asString() }
                .toList(),
        )
    }

    @Test
    fun `token list and card never carry the token string`() {
        for (name in listOf("EnrollmentToken", "EnrollmentTokenPage")) {
            val text = schema(name).toString()
            for (field in listOf("\"token\"", "enrollCommand", "secret")) {
                assertFalse(text.contains(field), "$name: $text")
            }
        }
        val created = schema("CreatedEnrollmentToken").path("required").strings()
        assertTrue(created.containsAll(listOf("id", "token", "enrollCommand", "expiresAt")), "$created")
    }

    @Test
    fun `a source requires its agent and repository`() {
        for (name in listOf("SourceInput", "Source")) {
            val required = schema(name).path("required").strings()
            assertTrue(required.containsAll(listOf("agentId", "repositoryName")), "$name: $required")
        }
    }

    /**
     * Every operation of the spec but session (W1b implemented it) is here, so a new
     * stub cannot skip this check.
     */
    @Test
    fun `stubs answer 501 with a problem until S8b`() {
        val id = "0192f7a0-0000-7000-8000-000000000001"
        val source = """{"name":"n","agentId":"$id","plugin":"files","repositoryName":"r","config":{}}"""
        val calls =
            listOf(
                Triple("GET", "/api/v1/agents?status=online", null),
                Triple("GET", "/api/v1/agents/{agentId}", null),
                Triple("POST", "/api/v1/enrollment-tokens", "{}"),
                Triple("GET", "/api/v1/enrollment-tokens?status=active", null),
                Triple("GET", "/api/v1/enrollment-tokens/{tokenId}", null),
                Triple("POST", "/api/v1/enrollment-tokens/{tokenId}/revoke", null),
                Triple("GET", "/api/v1/sources", null),
                Triple("POST", "/api/v1/sources", source),
                Triple("GET", "/api/v1/sources/{sourceId}", null),
                Triple("PUT", "/api/v1/sources/{sourceId}", source),
                Triple("DELETE", "/api/v1/sources/{sourceId}", null),
                Triple("POST", "/api/v1/sources/{sourceId}/runs", null),
                Triple("GET", "/api/v1/sources/{sourceId}/snapshots", null),
                Triple("GET", "/api/v1/runs?status=queued&status=failed", null),
                Triple("GET", "/api/v1/runs/{runId}", null),
                Triple("GET", "/api/v1/runs/{runId}/steps/{stepId}/logs", null),
            )
        val implemented = setOf("POST /api/v1/session", "GET /api/v1/session", "DELETE /api/v1/session")
        val stubbed = calls.map { "${it.first} ${it.second.substringBefore('?')}" }.toSet()
        assertEquals(operations().map { it.first }.toSet() - "GET /api/v1/status" - implemented, stubbed)
        val cookie = signIn()
        for ((method, path, body) in calls) {
            val response = send(method, path.replace(Regex("\\{[^}]+}"), id), body, cookie)
            assertEquals(501, response.statusCode(), "$method $path: ${response.body()}")
            assertEquals(PROBLEM_JSON, response.headers().firstValue("Content-Type").orElse(""), "$method $path")
            assertEquals("not_implemented", mapper.readTree(response.body()).path("code").asString(), "$method $path")
        }
    }

    private companion object {
        const val PROBLEM_JSON = "application/problem+json"
        const val NO_SESSION = "No session or it expired"
    }
}
