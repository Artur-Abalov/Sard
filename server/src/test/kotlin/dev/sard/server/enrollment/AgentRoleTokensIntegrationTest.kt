// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.enrollment

import dev.sard.server.TestcontainersConfiguration
import dev.sard.server.pki.AgentRole
import dev.sard.server.pki.MovableClock
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.context.annotation.Import
import org.springframework.jdbc.core.JdbcTemplate
import java.util.UUID
import kotlin.test.AfterTest
import kotlin.test.BeforeTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse

/** V1, rule "Роль задаётся токеном, по умолчанию source" (service level). */
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.RANDOM_PORT,
    properties = ["spring.grpc.server.port=0"],
)
@Import(TestcontainersConfiguration::class, EnrollmentTestConfiguration::class)
class AgentRoleTokensIntegrationTest(
    @Autowired private val tokens: EnrollmentTokens,
    @Autowired private val clock: MovableClock,
    @Autowired private val jdbc: JdbcTemplate,
) {
    private val acme = UUID.randomUUID()

    @BeforeTest
    fun `a fresh tenant`() {
        clock.now = ENROLLMENT_NOW
        jdbc.insertTenant(acme)
    }

    @AfterTest
    fun `drop the tenant`() = jdbc.deleteEnrollmentTenantData(acme)

    @Test
    fun `Токен без указанной роли создаётся с ролью source`() {
        val issued = tokens.create(acme)

        assertEquals(AgentRole.SOURCE, tokens.get(acme, issued.id)?.role)
    }

    @Test
    fun `Токен с ролью verifier создаётся с ролью verifier`() {
        val issued = tokens.create(acme, role = AgentRole.VERIFIER)

        assertEquals(AgentRole.VERIFIER, tokens.get(acme, issued.id)?.role)
        assertEquals(listOf(AgentRole.VERIFIER), tokens.list(acme).map { it.role })
    }

    @Test
    fun `Строка токена verifier имеет тот же формат, что у source`() {
        val source = tokens.create(acme).reveal()
        val verifier = tokens.create(acme, role = AgentRole.VERIFIER).reveal()

        val parsed = EnrollmentToken.parse(verifier)

        assertEquals(EnrollmentToken.parse(source).fingerprint, parsed.fingerprint)
        assertEquals(source.length, verifier.length)
        assertFalse("verifier" in verifier.lowercase(), verifier)
        assertEquals(verifier, parsed.encode())
    }

    @Test
    fun `Встроенный токен sard-self выпускается с ролью source`() {
        tokens.replaceBuiltin(acme)

        val roles =
            jdbc.queryForList(
                "select role from enrollment_tokens where tenant_id = ? and builtin",
                String::class.java,
                acme,
            )
        assertEquals(listOf("source"), roles)
    }
}
