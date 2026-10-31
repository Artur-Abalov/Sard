// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import dev.sard.server.enrollment.Enrollment
import dev.sard.server.enrollment.EnrollmentTokens
import dev.sard.server.extension.CommittedListener
import dev.sard.server.extension.EventLog
import dev.sard.server.extension.FailingListenersConfiguration
import dev.sard.server.extension.RecordingConfiguration
import dev.sard.server.extension.ThrowingListener
import dev.sard.server.pki.CertificateAuthority
import dev.sard.server.pki.MovableClock
import dev.sard.server.registration.Registration
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.beans.factory.annotation.Value
import org.springframework.boot.test.web.server.LocalServerPort
import org.springframework.context.annotation.Import
import org.springframework.jdbc.core.JdbcTemplate
import tools.jackson.databind.ObjectMapper
import java.util.UUID
import kotlin.test.AfterTest
import kotlin.test.BeforeTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

/** Rule "Событие доставляется синхронно после фиксации, не более одного раза, и слушатель не роняет операцию". */
@RestApiTest
@Import(RecordingConfiguration::class, FailingListenersConfiguration::class)
class EventDeliveryApiIntegrationTest(
    @Autowired ca: CertificateAuthority,
    @Autowired enrollment: Enrollment,
    @Autowired tokens: EnrollmentTokens,
    @Autowired registration: Registration,
    @Autowired jdbc: JdbcTemplate,
    @Autowired clock: MovableClock,
    @Autowired mapper: ObjectMapper,
    @Autowired private val log: EventLog,
    @Autowired private val throwing: ThrowingListener,
    @Autowired private val committed: CommittedListener,
    @LocalServerPort port: Int,
    @Value("\${local.grpc.server.port}") grpc: Int,
) {
    private val world = RestWorld(ca, enrollment, tokens, registration, jdbc, clock, mapper, port, grpc)
    private val tenant = world.tenant()
    private val admin = world.admin(tenant)
    private val agent = world.agent(tenant)

    @BeforeTest
    fun `no events yet`() {
        log.clear()
        committed.found.clear()
    }

    @AfterTest
    fun `drop the tenants`() = world.close()

    private fun defaults(
        backup: Int,
        restore: Int,
    ) = """{"backupTimeoutMinutes":$backup,"restoreTimeoutMinutes":$restore}"""

    @Test
    fun `Слушатель видит изменение уже зафиксированным`() {
        val created =
            world.api.send(
                "POST",
                "/api/v1/sources",
                admin,
                sourceJson("s-${UUID.randomUUID()}", agent.agentId),
            )

        assertEquals(201, created.status, created.toString())
        assertEquals(listOf(true), committed.found)
    }

    @Test
    fun `Ответ приходит после того, как слушатель получил событие`() {
        world.api.send("PUT", "/api/v1/run-defaults", admin, defaults(490, 610))

        assertEquals(1, log.settings("run_defaults").size)
    }

    @Test
    fun `Исключение слушателя не меняет ответ и исход операции`() {
        var response: ApiResponse? = null

        val logs = captureLogs { response = world.api.send("POST", "/api/v1/enrollment-tokens", admin, "{}") }

        assertEquals(201, response!!.status, response.toString())
        val id = response.json.path("id").asString()
        assertEquals(200, world.api.get("/api/v1/enrollment-tokens/$id", admin).status)
        assertTrue(logs.any { "listener failed on the event settings.changed" in it }, logs.toString())
    }

    @Test
    fun `Исключение слушателя не откатывает изменение настроек`() {
        val response = world.api.send("PUT", "/api/v1/run-defaults", admin, defaults(491, 611))

        assertEquals(200, response.status)
        assertEquals(
            491,
            world.api
                .get("/api/v1/run-defaults", admin)
                .json
                .path("backupTimeoutMinutes")
                .asInt(),
        )
    }

    @Test
    fun `Событие, на котором слушатель упал, не доставляется повторно`() {
        val before = throwing.calls.get()

        world.api.send("PUT", "/api/v1/run-defaults", admin, defaults(492, 612))
        Thread.sleep(SETTLE_MILLIS)

        assertEquals(before + 1, throwing.calls.get())
    }

    private companion object {
        const val SETTLE_MILLIS = 1500L
    }
}
