// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import org.mockito.Mockito
import org.springframework.mock.web.MockHttpServletRequest
import org.springframework.mock.web.MockHttpServletResponse
import org.springframework.web.servlet.HandlerMapping
import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertFailsWith
import kotlin.test.assertFalse
import kotlin.test.assertTrue

private val NO_TARGETS =
    object : GuardTargets {
        override fun isSystemSource(id: UUID) = false

        override fun isBuiltinAgent(id: UUID) = false
    }

private const val SOURCES = "/api/v1/sources"

class OnboardingGuardTest {
    private val changing = listOf("POST", "PUT", "PATCH", "DELETE")

    @Test
    fun `every method that is not safe is refused by the full restriction and documented as gated`() {
        changing.forEach { method ->
            val allowed = OnboardingRules.allowed(method, SOURCES, emptyMap(), OnboardingRestriction.FULL, NO_TARGETS)
            assertFalse(allowed, "$method allowed")
            assertTrue(OnboardingRules.gated(method, SOURCES), "$method not gated")
        }
    }

    @Test
    fun `safe methods are never gated`() {
        listOf("GET", "HEAD", "OPTIONS", "TRACE").forEach { assertFalse(OnboardingRules.gated(it, SOURCES), it) }
    }

    @Test
    fun `a PATCH during the full restriction throws OnboardingIncomplete`() {
        val onboarding = Mockito.mock(OnboardingApi::class.java)
        Mockito.`when`(onboarding.restriction()).thenReturn(OnboardingRestriction.FULL)
        val request = MockHttpServletRequest("PATCH", SOURCES)
        request.setAttribute(HandlerMapping.BEST_MATCHING_PATTERN_ATTRIBUTE, SOURCES)

        assertFailsWith<OnboardingIncomplete> {
            OnboardingGuard(onboarding, NO_TARGETS).preHandle(request, MockHttpServletResponse(), Any())
        }
    }
}
