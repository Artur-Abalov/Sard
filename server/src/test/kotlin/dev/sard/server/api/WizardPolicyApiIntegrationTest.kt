// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import dev.sard.server.enrollment.Enrollment
import dev.sard.server.enrollment.EnrollmentTokens
import dev.sard.server.extension.FixturePolicy
import dev.sard.server.extension.FixturePolicyConfiguration
import dev.sard.server.pki.CertificateAuthority
import dev.sard.server.pki.MovableClock
import dev.sard.server.registration.Registration
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.beans.factory.annotation.Value
import org.springframework.boot.test.web.server.LocalServerPort
import org.springframework.context.annotation.Import
import org.springframework.jdbc.core.JdbcTemplate
import tools.jackson.databind.ObjectMapper
import kotlin.test.AfterTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

/**
 * Rule "Без сессии открыто ровно то же, что раньше" (spring-security.feature, Р35): the steps of the wizard, which
 * the setup session guards in the controller, do not ask the access policy even when it forbids every operation.
 */
@RestApiTest
@Import(FixturePolicyConfiguration::class)
class WizardPolicyApiIntegrationTest(
    @Autowired ca: CertificateAuthority,
    @Autowired enrollment: Enrollment,
    @Autowired tokens: EnrollmentTokens,
    @Autowired registration: Registration,
    @Autowired jdbc: JdbcTemplate,
    @Autowired clock: MovableClock,
    @Autowired mapper: ObjectMapper,
    @Autowired private val policy: FixturePolicy,
    @LocalServerPort port: Int,
    @Value("\${local.grpc.server.port}") grpc: Int,
) {
    private val world = RestWorld(ca, enrollment, tokens, registration, jdbc, clock, mapper, port, grpc)

    @AfterTest
    fun `drop the tenants`() {
        policy.reset()
        world.close()
    }

    @Test
    fun `Шаги мастера с сессией настройки не спрашивают политику`() {
        policy.denyAll = true

        // The first sign-in on a clean installation runs the wizard: the code, the step ca, the step admin.
        val admin = world.admin()

        assertTrue(admin.cookie.isNotEmpty())
        assertEquals(emptyList(), policy.calls.map { it.second.toString() })
    }
}
