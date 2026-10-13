// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import io.swagger.v3.oas.models.OpenAPI
import io.swagger.v3.oas.models.Operation
import io.swagger.v3.oas.models.PathItem
import io.swagger.v3.oas.models.Paths
import io.swagger.v3.oas.models.media.Content
import io.swagger.v3.oas.models.media.MediaType
import io.swagger.v3.oas.models.media.Schema
import io.swagger.v3.oas.models.responses.ApiResponse
import io.swagger.v3.oas.models.responses.ApiResponses
import org.mockito.Mockito
import org.springframework.mock.web.MockHttpServletRequest
import org.springframework.mock.web.MockHttpServletResponse
import org.springframework.web.servlet.HandlerMapping
import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertFalse
import kotlin.test.assertTrue

private val NO_TARGETS =
    object : GuardTargets {
        override fun isSystemSource(id: UUID) = false

        override fun isBuiltinAgent(id: UUID) = false
    }

private const val SOURCES = "/api/v1/sources"
private const val ISSUE = "/api/v1/enrollment-tokens"

@MutFlowTest
class OnboardingGuardTest {
    private val changing = listOf("POST", "PUT", "PATCH", "DELETE")

    private fun gated(
        method: String,
        template: String,
    ) = MutFlow.underTest { OnboardingRules.gated(method, template) }

    @Test
    fun `every method that is not safe is refused by the full restriction and documented as gated`() {
        changing.forEach { method ->
            val allowed =
                MutFlow.underTest {
                    OnboardingRules.allowed(method, SOURCES, emptyMap(), OnboardingRestriction.FULL, NO_TARGETS)
                }
            assertFalse(allowed, "$method allowed")
            assertTrue(gated(method, SOURCES), "$method not gated")
        }
    }

    @Test
    fun `safe methods are never gated`() {
        listOf("GET", "HEAD", "OPTIONS", "TRACE").forEach { assertFalse(gated(it, SOURCES), it) }
    }

    @Test
    fun `a PATCH during the full restriction throws OnboardingIncomplete`() {
        val onboarding = Mockito.mock(OnboardingApi::class.java)
        Mockito.`when`(onboarding.restriction()).thenReturn(OnboardingRestriction.FULL)
        val request = MockHttpServletRequest("PATCH", SOURCES)
        request.setAttribute(HandlerMapping.BEST_MATCHING_PATTERN_ATTRIBUTE, SOURCES)

        assertFailsWith<OnboardingIncomplete> {
            MutFlow.underTest {
                OnboardingGuard(onboarding, NO_TARGETS).preHandle(request, MockHttpServletResponse(), Any())
            }
        }
    }

    // ---- the rules, one by one ----

    private val system = UUID.fromString("00000000-0000-0000-0000-0000000000a1")
    private val builtin = UUID.fromString("00000000-0000-0000-0000-0000000000b2")
    private val other = UUID.fromString("00000000-0000-0000-0000-0000000000c3")
    private val targets =
        object : GuardTargets {
            override fun isSystemSource(id: UUID) = id == system

            override fun isBuiltinAgent(id: UUID) = id == builtin
        }
    private val schedule = "/api/v1/sources/{sourceId}/schedule"
    private val revoke = "/api/v1/agents/{agentId}/revoke"
    private val alwaysOpen =
        listOf(
            "POST" to "/api/v1/session",
            "DELETE" to "/api/v1/session",
            "PUT" to "/api/v1/session/password",
            "PUT" to "/api/v1/self-backup/repository",
            "POST" to "/api/v1/self-backup/runs",
            "POST" to "/api/v1/self-backup/keys-confirmation",
        )

    private fun allowed(
        restriction: OnboardingRestriction,
        method: String,
        template: String,
        variables: Map<String, String> = emptyMap(),
    ) = MutFlow.underTest { OnboardingRules.allowed(method, template, variables, restriction, targets) }

    @Test
    fun `no restriction lets every change through`() {
        for ((method, template) in listOf("POST" to SOURCES, "PATCH" to SOURCES, "POST" to ISSUE)) {
            assertTrue(allowed(OnboardingRestriction.NONE, method, template), "$method $template")
        }
    }

    @Test
    fun `the new-agents restriction refuses only the issue of an enrollment token`() {
        assertFalse(allowed(OnboardingRestriction.NEW_AGENTS, "POST", ISSUE))
        assertTrue(allowed(OnboardingRestriction.NEW_AGENTS, "PUT", ISSUE))
        assertTrue(allowed(OnboardingRestriction.NEW_AGENTS, "POST", "$ISSUE/{tokenId}/revoke"))
        assertTrue(allowed(OnboardingRestriction.NEW_AGENTS, "POST", SOURCES))
        assertTrue(allowed(OnboardingRestriction.NEW_AGENTS, "DELETE", SOURCES))
    }

    @Test
    fun `the full restriction lets through exactly the always-open operations`() {
        for ((method, template) in alwaysOpen) {
            assertTrue(allowed(OnboardingRestriction.FULL, method, template), "$method $template")
            assertFalse(gated(method, template), "$method $template gated")
            for (wrong in changing.filter { it != method && (it to template) !in alwaysOpen }) {
                assertFalse(allowed(OnboardingRestriction.FULL, wrong, template), "$wrong $template")
                assertTrue(gated(wrong, template), "$wrong $template not gated")
            }
        }
    }

    @Test
    fun `the full restriction lets through every operation of the onboarding and gates nothing there`() {
        for (method in changing) {
            val template = "/api/v1/onboarding/ca"
            assertTrue(allowed(OnboardingRestriction.FULL, method, template), method)
            assertFalse(gated(method, template), method)
        }
        assertFalse(allowed(OnboardingRestriction.FULL, "POST", "/api/v1/onboarding"))
        assertTrue(gated("POST", "/api/v1/onboarding"))
        assertFalse(allowed(OnboardingRestriction.FULL, "POST", "/api/v1/x/api/v1/onboarding/ca"))
        assertTrue(gated("POST", "/api/v1/x/api/v1/onboarding/ca"))
    }

    @Test
    fun `the operations that only some restrictions refuse are gated`() {
        assertTrue(gated("POST", ISSUE))
        assertTrue(gated("PUT", schedule))
        assertTrue(gated("POST", revoke))
        assertTrue(gated("DELETE", "/api/v1/session/other"))
    }

    @Test
    fun `the schedule of a system source passes the full restriction, of any other source does not`() {
        val full = OnboardingRestriction.FULL
        assertTrue(allowed(full, "PUT", schedule, mapOf("sourceId" to system.toString())))
        assertFalse(allowed(full, "PUT", schedule, mapOf("sourceId" to other.toString())))
        assertFalse(allowed(full, "PUT", schedule, mapOf("sourceId" to "not-a-uuid")))
        assertFalse(allowed(full, "PUT", schedule, emptyMap()))
        assertFalse(allowed(full, "PUT", schedule, mapOf("agentId" to system.toString())))
        assertFalse(allowed(full, "PUT", schedule, mapOf("sourceId" to builtin.toString())))
        for (method in changing - "PUT") {
            assertFalse(allowed(full, method, schedule, mapOf("sourceId" to system.toString())), method)
        }
        assertFalse(allowed(full, "PUT", "$schedule/x", mapOf("sourceId" to system.toString())))
    }

    @Test
    fun `the revocation of the built-in agent passes the full restriction, of any other agent does not`() {
        val full = OnboardingRestriction.FULL
        assertTrue(allowed(full, "POST", revoke, mapOf("agentId" to builtin.toString())))
        assertFalse(allowed(full, "POST", revoke, mapOf("agentId" to other.toString())))
        assertFalse(allowed(full, "POST", revoke, mapOf("agentId" to "not-a-uuid")))
        assertFalse(allowed(full, "POST", revoke, emptyMap()))
        assertFalse(allowed(full, "POST", revoke, mapOf("sourceId" to builtin.toString())))
        assertFalse(allowed(full, "POST", revoke, mapOf("agentId" to system.toString())))
        for (method in changing - "POST") {
            assertFalse(allowed(full, method, revoke, mapOf("agentId" to builtin.toString())), method)
        }
        assertFalse(allowed(full, "POST", "$revoke/x", mapOf("agentId" to builtin.toString())))
    }

    // ---- the interceptor ----

    private fun guard(restriction: OnboardingRestriction): OnboardingGuard {
        val onboarding = Mockito.mock(OnboardingApi::class.java)
        Mockito.`when`(onboarding.restriction()).thenReturn(restriction)
        return OnboardingGuard(onboarding, targets)
    }

    private fun request(
        method: String,
        template: String? = SOURCES,
        variables: Any? = null,
    ): MockHttpServletRequest {
        val request = MockHttpServletRequest(method, SOURCES)
        template?.let { request.setAttribute(HandlerMapping.BEST_MATCHING_PATTERN_ATTRIBUTE, it) }
        variables?.let { request.setAttribute(HandlerMapping.URI_TEMPLATE_VARIABLES_ATTRIBUTE, it) }
        return request
    }

    private fun preHandle(
        restriction: OnboardingRestriction,
        request: MockHttpServletRequest,
    ) = MutFlow.underTest { guard(restriction).preHandle(request, MockHttpServletResponse(), Any()) }

    @Test
    fun `a safe request passes under every restriction`() {
        for (restriction in OnboardingRestriction.entries) {
            for (method in listOf("GET", "HEAD", "OPTIONS", "TRACE")) {
                assertTrue(preHandle(restriction, request(method)), "$method under $restriction")
            }
        }
    }

    @Test
    fun `a safe request passes even without a matched pattern`() {
        assertTrue(preHandle(OnboardingRestriction.FULL, request("GET", template = null)))
    }

    @Test
    fun `a change without a matched pattern is refused under a restriction and passes without one`() {
        for (restriction in listOf(OnboardingRestriction.NEW_AGENTS, OnboardingRestriction.FULL)) {
            assertFailsWith<OnboardingIncomplete>(restriction.toString()) {
                preHandle(restriction, request("POST", template = null))
            }
        }
        assertTrue(preHandle(OnboardingRestriction.NONE, request("POST", template = null)))
    }

    @Test
    fun `a change under no restriction passes whatever the pattern`() {
        assertTrue(preHandle(OnboardingRestriction.NONE, request("POST", ISSUE)))
    }

    @Test
    fun `the interceptor decides on the path variables of the matched pattern`() {
        val systemSchedule = request("PUT", schedule, mapOf("sourceId" to system.toString()))
        assertTrue(preHandle(OnboardingRestriction.FULL, systemSchedule))
        val builtinRevoke = request("POST", revoke, mapOf("agentId" to builtin.toString()))
        assertTrue(preHandle(OnboardingRestriction.FULL, builtinRevoke))
        val otherSchedule = request("PUT", schedule, mapOf("sourceId" to other.toString()))
        assertFailsWith<OnboardingIncomplete> { preHandle(OnboardingRestriction.FULL, otherSchedule) }
    }

    @Test
    fun `path variables that are not a map decide nothing`() {
        assertFailsWith<OnboardingIncomplete> {
            preHandle(OnboardingRestriction.FULL, request("PUT", schedule, "sourceId=$system"))
        }
        assertFailsWith<OnboardingIncomplete> { preHandle(OnboardingRestriction.FULL, request("PUT", schedule)) }
    }

    @Test
    fun `the new-agents restriction refuses the issue of a token and lets a change of a source through`() {
        assertFailsWith<OnboardingIncomplete> { preHandle(OnboardingRestriction.NEW_AGENTS, request("POST", ISSUE)) }
        assertTrue(preHandle(OnboardingRestriction.NEW_AGENTS, request("POST", SOURCES)))
    }

    // ---- the 409 in the OpenAPI document ----

    private val problemJson = "application/problem+json"

    private fun operation(conflict: ApiResponse? = null): Operation {
        val responses = ApiResponses().addApiResponse("200", ApiResponse().description("ok"))
        conflict?.let { responses.addApiResponse("409", it) }
        return Operation().responses(responses)
    }

    private fun document(vararg paths: Pair<String, PathItem>): OpenAPI {
        val api = OpenAPI()
        api.paths = Paths().also { all -> paths.forEach { (path, item) -> all.addPathItem(path, item) } }
        return api
    }

    private fun described(api: OpenAPI) = MutFlow.underTest { describeOnboardingIncomplete(api) }

    @Test
    fun `a gated operation without its own 409 gets the plain problem`() {
        val api = document(SOURCES to PathItem().post(operation()))

        described(api)

        val conflict =
            api.paths
                .getValue(SOURCES)
                .post.responses
                .getValue("409")
        assertEquals(
            "The onboarding is not complete (onboarding_incomplete); or the onboarding is not complete",
            conflict.description,
        )
        assertEquals(
            "#/components/schemas/Problem",
            conflict.content
                .getValue(problemJson)
                .schema.`$ref`,
        )
        assertEquals(setOf(problemJson), conflict.content.keys)
        assertEquals(
            "ok",
            api.paths
                .getValue(SOURCES)
                .post.responses
                .getValue("200")
                .description,
        )
    }

    @Test
    fun `a gated operation with its own 409 keeps it and the response holds either body`() {
        val own = Schema<Any>().`$ref`("RunActiveProblem")
        val content = Content().addMediaType(problemJson, MediaType().schema(own))
        val conflict = ApiResponse().description("A run is active").content(content)
        val api = document("/api/v1/sources/{id}/runs" to PathItem().post(operation(conflict)))

        described(api)

        val merged =
            api.paths
                .getValue("/api/v1/sources/{id}/runs")
                .post.responses
                .getValue("409")
        assertEquals("A run is active; or the onboarding is not complete", merged.description)
        val either =
            merged.content
                .getValue(problemJson)
                .schema.anyOf
        val refs = either.map { it.`$ref` }
        assertEquals(listOf("#/components/schemas/RunActiveProblem", "#/components/schemas/Problem"), refs)
    }

    @Test
    fun `an own 409 without a problem body is replaced by the plain problem but keeps its description`() {
        val other = Content().addMediaType("text/plain", MediaType().schema(Schema<Any>().`$ref`("Other")))
        val bare = ApiResponse().description("Conflict of its own")
        val text = ApiResponse().description("Text conflict").content(other)
        val api =
            document(
                "/api/v1/a" to PathItem().post(operation(bare)),
                "/api/v1/b" to PathItem().put(operation(text)),
            )

        described(api)

        val a =
            api.paths
                .getValue("/api/v1/a")
                .post.responses
                .getValue("409")
        assertEquals("Conflict of its own; or the onboarding is not complete", a.description)
        assertEquals(
            "#/components/schemas/Problem",
            a.content
                .getValue(problemJson)
                .schema.`$ref`,
        )
        val b =
            api.paths
                .getValue("/api/v1/b")
                .put.responses
                .getValue("409")
        assertEquals("Text conflict; or the onboarding is not complete", b.description)
        assertEquals(
            "#/components/schemas/Problem",
            b.content
                .getValue(problemJson)
                .schema.`$ref`,
        )
    }

    @Test
    fun `only the gated operations of a path are described`() {
        val item =
            PathItem()
                .get(operation())
                .post(operation())
                .delete(operation())
                .patch(operation())
        val open = PathItem().post(operation())
        val onboarding = PathItem().post(operation())
        val api = document(SOURCES to item, "/api/v1/session" to open, "/api/v1/onboarding/ca" to onboarding)

        described(api)

        val sources = api.paths.getValue(SOURCES)
        assertEquals(null, sources.get.responses["409"])
        val changed = listOf(sources.post, sources.delete, sources.patch)
        assertEquals(listOf(true, true, true), changed.map { it.responses.containsKey("409") })
        assertEquals(
            false,
            api.paths
                .getValue("/api/v1/session")
                .post.responses
                .containsKey("409"),
        )
        assertEquals(
            false,
            api.paths
                .getValue("/api/v1/onboarding/ca")
                .post.responses
                .containsKey("409"),
        )
    }

    @Test
    fun `a document without paths is left alone`() {
        val api = OpenAPI()

        described(api)

        assertEquals(null, api.paths)
    }
}
