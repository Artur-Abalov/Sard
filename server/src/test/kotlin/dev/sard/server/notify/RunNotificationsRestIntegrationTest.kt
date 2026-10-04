// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.notify

import dev.sard.server.api.ApiSession
import dev.sard.server.api.RestApiTest
import dev.sard.server.api.RestWorld
import dev.sard.server.api.sourceJson
import dev.sard.server.enrollment.Enrollment
import dev.sard.server.enrollment.EnrollmentTokens
import dev.sard.server.notify.telegram.FakeBotApi
import dev.sard.server.notify.telegram.TEST_TOKEN
import dev.sard.server.persistence.TenantSessions
import dev.sard.server.persistence.UuidV7
import dev.sard.server.pki.CertificateAuthority
import dev.sard.server.pki.MovableClock
import dev.sard.server.registration.Registration
import org.junit.jupiter.api.AfterAll
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.beans.factory.annotation.Value
import org.springframework.boot.test.web.server.LocalServerPort
import org.springframework.jdbc.core.JdbcTemplate
import org.springframework.test.context.DynamicPropertyRegistry
import org.springframework.test.context.DynamicPropertySource
import org.springframework.test.context.TestPropertySource
import tools.jackson.databind.ObjectMapper
import tools.jackson.databind.json.JsonMapper
import java.net.Socket
import java.security.SecureRandom
import java.time.Duration
import java.util.UUID
import kotlin.test.AfterTest
import kotlin.test.BeforeTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

private const val CONSOLE = "https://sard.example.com"
private const val EVIL = "evil.example"
private val JSON = JsonMapper.builder().build()

/** Sends the request as a client of a hostile proxy would: the Host and X-Forwarded-Host headers name [EVIL]. */
private fun startWithHostileHeaders(
    port: Int,
    session: ApiSession,
    source: String,
): String =
    Socket("localhost", port).use { socket ->
        val request =
            "POST /api/v1/sources/$source/runs HTTP/1.1\r\nHost: $EVIL\r\nX-Forwarded-Host: $EVIL\r\n" +
                "Forwarded: host=$EVIL\r\nCookie: sard_session=${session.cookie}\r\nContent-Length: 0\r\n" +
                "Connection: close\r\n\r\n"
        socket.getOutputStream().apply {
            write(request.toByteArray())
            flush()
        }
        socket.getInputStream().readAllBytes().decodeToString()
    }

/**
 * S9b, "Ссылка на консоль берётся только из настройки публичного адреса": the run is started over
 * REST with forged Host headers; the message must not learn the address from them.
 */
@RestApiTest
@TestPropertySource(
    properties = [
        "sard.notify.tick-interval=1h",
        "SARD_TELEGRAM_BOT_TOKEN=$TEST_TOKEN",
        "SARD_TELEGRAM_CHAT_ID=-100777",
        "SARD_CONSOLE_PUBLIC_URL=$CONSOLE",
    ],
)
class RunNotificationsRestIntegrationTest(
    @Autowired ca: CertificateAuthority,
    @Autowired enrollment: Enrollment,
    @Autowired tokens: EnrollmentTokens,
    @Autowired registration: Registration,
    @Autowired private val jdbc: JdbcTemplate,
    @Autowired private val clock: MovableClock,
    @Autowired mapper: ObjectMapper,
    @Autowired private val loop: NotificationLoop,
    @Autowired private val service: NotificationService,
    @Autowired private val sessions: TenantSessions,
    @LocalServerPort private val port: Int,
    @Value("\${local.grpc.server.port}") grpc: Int,
) {
    private val world = RestWorld(ca, enrollment, tokens, registration, jdbc, clock, mapper, port, grpc)
    private val tenant = world.tenant()
    private val admin = world.admin(tenant)
    private val agent = world.agent(tenant)

    companion object {
        private val fake = FakeBotApi()

        @JvmStatic
        @DynamicPropertySource
        fun botApi(registry: DynamicPropertyRegistry) {
            registry.add("sard.notify.telegram.api-url") { fake.uri.toString() }
        }

        @JvmStatic
        @AfterAll
        fun closeFake() {
            fake.close()
        }
    }

    @BeforeTest
    fun `the test drives the ticks`() {
        loop.stop()
        fake.requests.clear()
    }

    @AfterTest
    fun `drop the world`() {
        jdbc.update("delete from notification_deliveries where tenant_id = ?", tenant)
        world.close()
    }

    /** Starts a run with hostile headers and lets it succeed; returns its id. */
    private fun succeededRun(): UUID {
        val source =
            world.api
                .post("/api/v1/sources", admin, sourceJson("db-main", agent.agentId))
                .json
                .path("id")
                .asString()
        val response = startWithHostileHeaders(port, admin, source)
        assertTrue(response.startsWith("HTTP/1.1 201"), response)
        val run = jdbc.queryForObject("select id from runs where tenant_id = ?", UUID::class.java, tenant)!!
        world.forceRun(run, "succeeded")
        return run
    }

    @Test
    fun `Ссылка строится из настройки, а не из заголовков запроса`() {
        val run = succeededRun()

        service.tick()

        val shown = JSON.readTree(fake.requests.single().body).path("text").asString()
        assertFalse(EVIL in shown, shown)
        assertEquals("$CONSOLE/runs/$run", shown.lines().last())
    }

    @Test
    fun `Адрес входящего запроса в ссылку не попадает`() {
        succeededRun()
        val channel = CapturingChannel()
        val withoutLink =
            NotificationService(
                Deliveries(sessions, UuidV7(clock, SecureRandom())),
                listOf(channel),
                RunNoticeFormatter(NoticeLanguage.EN, ConsoleUrl.parse("")),
                RetryPolicy(RetrySettings()),
                QueueSettings(batch = 10, lease = Duration.ofMinutes(5), ttl = Duration.ofHours(24)),
                clock,
            )

        withoutLink.tick()

        val shown = channel.sent.single().plainText()
        assertFalse(EVIL in shown || "http" in shown, shown)
    }
}
