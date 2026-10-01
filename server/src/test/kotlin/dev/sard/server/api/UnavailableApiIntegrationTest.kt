// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import dev.sard.server.agents.dispatch.ReconciledHellos
import dev.sard.server.enrollment.Enrollment
import dev.sard.server.enrollment.EnrollmentTokens
import dev.sard.server.persistence.TenantSessions
import dev.sard.server.pki.CertificateAuthority
import dev.sard.server.pki.MovableClock
import dev.sard.server.registration.Registration
import org.hibernate.Session
import org.hibernate.exception.JDBCConnectionException
import org.mockito.ArgumentMatchers
import org.mockito.Mockito
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.beans.factory.annotation.Value
import org.springframework.boot.test.web.server.LocalServerPort
import org.springframework.jdbc.core.JdbcTemplate
import org.springframework.test.context.bean.override.mockito.MockitoSpyBean
import tools.jackson.databind.ObjectMapper
import java.sql.SQLException
import java.util.UUID
import kotlin.test.AfterTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

private const val FAILURE = "disk on fire"

/** The database is down: 503 without details, nothing half done (rules "Ошибки", "Отзыв агента", "Токены"). */
@RestApiTest
class UnavailableApiIntegrationTest(
    @Autowired ca: CertificateAuthority,
    @Autowired enrollment: Enrollment,
    @Autowired tokens: EnrollmentTokens,
    @Autowired registration: Registration,
    @Autowired jdbc: JdbcTemplate,
    @Autowired clock: MovableClock,
    @Autowired mapper: ObjectMapper,
    @Autowired private val hellos: ReconciledHellos,
    @LocalServerPort port: Int,
    @Value("\${local.grpc.server.port}") grpc: Int,
) {
    @MockitoSpyBean
    lateinit var sessions: TenantSessions

    private val world = RestWorld(ca, enrollment, tokens, registration, jdbc, clock, mapper, port, grpc)
    private val tenant = world.tenant()
    private val admin = world.admin(tenant)

    @AfterTest
    fun `the database is back, drop the tenants`() {
        Mockito.reset(sessions)
        world.close()
    }

    /** Every database access of the server fails as a lost connection would. */
    private fun databaseDown() {
        val failure = JDBCConnectionException(FAILURE, SQLException(FAILURE))
        Mockito
            .doThrow(failure)
            .`when`(sessions)
            .inTenant(anything(UUID.randomUUID()), anything<(Session) -> Any?> { null })
    }

    /** A Mockito matcher that Kotlin accepts for a parameter that is not null: [placeholder] is never used. */
    private fun <T : Any> anything(placeholder: T): T {
        ArgumentMatchers.any<Any>()
        return placeholder
    }

    private fun databaseUp() = Mockito.reset(sessions)

    @Test
    fun `Недоступная база данных даёт 503 без подробностей`() {
        databaseDown()
        var response: ApiResponse? = null

        val logs = captureLogs { response = world.api.get("/api/v1/agents", admin) }

        assertEquals(503, response?.status)
        assertEquals("unavailable", response?.code)
        assertFalse(FAILURE in response!!.body)
        assertTrue(logs.any { FAILURE in it }, "the log lacks the reason: $logs")
    }

    @Test
    fun `Отказ базы при создании токена не выдаёт строку`() {
        databaseDown()

        val response = world.api.post("/api/v1/enrollment-tokens", admin)

        assertEquals(503, response.status)
        assertEquals("unavailable", response.code)
        assertTrue(response.json.path("token").isMissingNode && response.json.path("enrollCommand").isMissingNode)
        databaseUp()
        assertEquals(0, world.count("enrollment_tokens", tenant))
    }

    @Test
    fun `Отказ базы при отзыве агента не отзывает его и не закрывает стрим`() {
        val agent = world.agent(tenant)
        val stream = world.connect(agent).also { it.hello() }
        hellos.await(agent.agentId)
        databaseDown()

        val response = world.api.post("/api/v1/agents/${agent.agentId}/revoke", admin, null)

        assertEquals(503, response.status)
        assertEquals("unavailable", response.code)
        Thread.sleep(QUIET_MILLIS)
        assertTrue(stream.isOpen)
        databaseUp()
        assertTrue(
            world.jdbc.queryForObject("select revoked_at from agents where id = ?", java.sql.Timestamp::class.java, agent.agentId) == null,
        )
        assertEquals(
            0,
            world.jdbc.queryForObject(
                "select count(*) from agent_certificates where agent_id = ? and revoked_at is not null",
                Int::class.java,
                agent.agentId,
            ),
        )
    }

    private companion object {
        const val QUIET_MILLIS = 300L
    }
}
