// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import dev.sard.server.auth.PUBLIC_OPERATIONS
import dev.sard.server.onboarding.FirstStartClient
import dev.sard.server.onboarding.FirstStartTest
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.test.web.server.LocalServerPort
import tools.jackson.databind.JsonNode
import tools.jackson.databind.ObjectMapper
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

/**
 * The rules about the OpenAPI document in docs/specs/server/onboarding-setup.feature (К1-К9), read from the
 * served document.
 */
@FirstStartTest
class OnboardingContractIntegrationTest(
    @Autowired private val mapper: ObjectMapper,
    @LocalServerPort port: Int,
) {
    private val client = FirstStartClient(port, mapper)
    private val spec: JsonNode by lazy { mapper.readTree(client.send("GET", "/v3/api-docs").body) }

    private fun operations(): List<Pair<String, JsonNode>> =
        spec.path("paths").properties().flatMap { (path, item) ->
            item.properties().map { (method, op) -> "${method.uppercase()} $path" to op }
        }

    private fun wire(schema: String) =
        spec
            .path("components")
            .path("schemas")
            .path(schema)
            .path("enum")
            .list()
            .map { it.asString() }

    @Test
    fun `Спецификация объявляет публичными ровно четыре операции`() {
        val emptySecurity = operations().filter { (_, op) -> op.path("security").let { it.isArray && it.isEmpty } }
        val public = emptySecurity.map { it.first }.toSet()

        assertEquals(
            setOf(
                "POST /api/v1/session",
                "GET /api/v1/status",
                "GET /api/v1/onboarding",
                "POST /api/v1/onboarding/setup-session",
            ),
            public,
        )
        assertEquals(PUBLIC_OPERATIONS, public)
    }

    @Test
    fun `Требование setupSession есть ровно у шагов ca и admin`() {
        val withSetup =
            operations()
                .filter { (_, op) -> op.path("security").list().any { it.has("setupSession") } }
                .map { it.first }
                .toSet()

        assertEquals(setOf("POST /api/v1/onboarding/ca", "POST /api/v1/onboarding/admin"), withSetup)
    }

    @Test
    fun `Спецификация описывает cookie сессии настройки и новые коды ошибок`() {
        val scheme = spec.path("components").path("securitySchemes").path("setupSession")
        val described = listOf("type", "in", "name").map { scheme.path(it).asString() }
        assertEquals(listOf("apiKey", "cookie", "sard_setup"), described)
        val header =
            spec
                .path("paths")
                .path("/api/v1/onboarding/setup-session")
                .path("post")
                .path("responses")
                .path("204")
                .path("headers")
                .path("Set-Cookie")
                .path("description")
                .asString()
        for (part in listOf("sard_setup", "HttpOnly", "SameSite=Strict", "Path=/api/v1/onboarding", "Secure")) {
            assertTrue(part in header, "$part: $header")
        }
        val codes = wire("ErrorCode")
        for (code in listOf("setup_required", "setup_completed", "ca_step_pending", "wrong_password")) {
            assertTrue(code in codes, code)
        }
        for (path in listOf("setup-session", "ca", "admin")) {
            val declared =
                spec
                    .path("paths")
                    .path("/api/v1/onboarding/$path")
                    .path("post")
                    .path("responses")
            assertTrue(declared.has("403"), path)
        }
    }

    @Test
    fun `Ни одна операция API не сбрасывает пароль администратора`() {
        val setting =
            operations()
                .filter { (_, op) ->
                    val schema =
                        op
                            .path("requestBody")
                            .path("content")
                            .path("application/json")
                            .path("schema")
                            .path("\$ref")
                            .asString()
                    schema.endsWith("/AdminStepRequest") || schema.endsWith("/PasswordChangeRequest")
                }.map { it.first }
                .toSet()

        assertEquals(setOf("POST /api/v1/onboarding/admin", "PUT /api/v1/session/password"), setting)
        val deleting = operations().map { it.first }.filter { it.startsWith("DELETE") }
        assertTrue(deleting.none { "password" in it || "onboarding" in it })
    }

    @Test
    fun `Значения перечислений мастера в спецификации — те, что уходят по сети`() {
        assertEquals(listOf("ca", "admin", "self_backup", "keys_confirmed"), wire("OnboardingStepId"))
        assertEquals(listOf("done", "pending", "upcoming"), wire("OnboardingStepState"))
        assertEquals(listOf("active", "expired", "not_issued"), wire("SetupCodeState"))
        assertEquals(listOf("none", "setup", "admin"), wire("OnboardingAccess"))
        assertEquals(listOf("generated", "imported"), wire("CaOrigin"))
        for (entries in listOf(
            OnboardingStepId.entries,
            OnboardingStepState.entries,
            SetupCodeState.entries,
            OnboardingAccess.entries,
            CaOrigin.entries,
        )) {
            val name = entries.first().javaClass.simpleName
            assertEquals(wire(name), entries.map { mapper.readTree(mapper.writeValueAsString(it)).asString() }, name)
        }
    }

    @Test
    fun `Ответы мастера соответствуют схемам спецификации`() {
        val contract = ContractCheck(spec, mapper)

        fun check(
            method: String,
            path: String,
            reply: dev.sard.server.onboarding.Reply,
        ) = contract.check(method, path, ApiResponse(reply.status, reply.contentType, reply.body, reply.json))

        check("GET", "/api/v1/onboarding", client.state())
        val setup = client.setupSession()
        check("GET", "/api/v1/onboarding", client.state(setup))
        check("POST", "/api/v1/onboarding/setup-session", client.enterCode("0000-0000-0000-0000-0000-0000-0000"))
        check("POST", "/api/v1/onboarding/ca", client.confirmCa(setup))
        check("POST", "/api/v1/onboarding/admin", client.admin(setup, "short"))
        check("POST", "/api/v1/session", client.login("correct-horse-battery"))
        client.admin(setup, "correct-horse-battery")
        val admin = client.login("correct-horse-battery").cookie(SESSION_COOKIE)
        val change = client.changePassword(admin, "wrong-password-123", "new-password-2026")
        check("PUT", "/api/v1/session/password", change)
    }
}
