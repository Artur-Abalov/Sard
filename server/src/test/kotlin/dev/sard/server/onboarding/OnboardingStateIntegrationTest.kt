// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.onboarding

import dev.sard.server.api.SESSION_COOKIE
import dev.sard.server.api.SETUP_COOKIE
import dev.sard.server.api.list
import dev.sard.server.auth.JdbcAdministrators
import dev.sard.server.auth.LoginAttemptTracker
import dev.sard.server.auth.PasswordHasher
import dev.sard.server.auth.SessionStore
import dev.sard.server.pki.CertificateAuthority
import dev.sard.server.pki.MovableClock
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.beans.factory.annotation.Value
import org.springframework.boot.test.web.server.LocalServerPort
import org.springframework.jdbc.core.JdbcTemplate
import tools.jackson.databind.ObjectMapper
import java.nio.file.Path
import java.time.Duration
import java.util.UUID
import java.util.concurrent.CountDownLatch
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit
import kotlin.test.BeforeTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNotEquals
import kotlin.test.assertNull
import kotlin.test.assertTrue

private const val EVIL = "https://evil.example"
private val TENANT_B = UUID.fromString("00000000-0000-0000-0000-0000000000b4")

/**
 * Rules "Состояние онбординга публично…", "Шаг ca выполняется явным подтверждением CA…", "Шаг admin задаёт пароль
 * и сразу выдаёт сессию администратора", "Пароль хранится только как хэш Argon2id", "Вход до создания
 * администратора отвечает 409 setup_required" and "До шага admin открыты только статус, состояние онбординга и
 * ввод кода" of docs/specs/server/onboarding-setup.feature (@http).
 */
@FirstStartTest
class OnboardingStateIntegrationTest(
    @Autowired private val jdbc: JdbcTemplate,
    @Autowired private val clock: MovableClock,
    @Autowired codes: SetupCodes,
    @Autowired sessions: SetupSessions,
    @Autowired codeAttempts: SetupCodeAttempts,
    @Autowired adminSessions: SessionStore,
    @Autowired signInAttempts: LoginAttemptTracker,
    @Autowired private val ca: CertificateAuthority,
    @Autowired mapper: ObjectMapper,
    @Value("\${sard.pki.dir}") private val pkiDir: Path,
    @LocalServerPort port: Int,
) {
    private val client = FirstStartClient(port, mapper)
    private val installation = CleanInstallation(jdbc, clock, codes, sessions, codeAttempts, adminSessions, signInAttempts)
    private val fingerprint get() = ca.fingerprint().hex
    private val keyPath get() =
        pkiDir
            .toAbsolutePath()
            .normalize()
            .resolve("ca/ca.key")
            .toString()

    @BeforeTest
    fun `a clean installation with the code C`() = installation.restore()

    private fun steps(reply: Reply) =
        reply.json
            .path("steps")
            .list()
            .map { it.path("id").asString() to it.path("state").asString() }

    private fun stepStates(reply: Reply) = steps(reply).map { it.second }

    private fun setupReady(): String = client.setupSession().also { assertEquals(204, client.confirmCa(it).status) }

    // ---- Правило: Состояние онбординга публично, сведения о CA только с сессией ----

    @Test
    fun `Состояние чистой установки без cookie перечисляет шаги и не раскрывает CA`() {
        val reply = client.state()

        assertEquals(200, reply.status)
        val expected =
            """{"steps": [{"id": "ca", "state": "pending"}, {"id": "admin", "state": "pending"},
               {"id": "self_backup", "state": "upcoming"}, {"id": "keys_confirmed", "state": "upcoming"}],
               "setupCode": "active", "access": "none", "ca": null, "caReplaceable": null}"""
        assertEquals(client.jsonOf(expected), reply.json)
        assertFalse(fingerprint in reply.body)
    }

    @Test
    fun `Сессия настройки видит CA сервера и где лежит его ключ`() {
        val session = client.setupSession()

        val state = client.state(session)

        assertEquals("setup", state.json.path("access").asString())
        val ca = state.json.path("ca")
        assertEquals(fingerprint, ca.path("fingerprint").asString())
        assertEquals("generated", ca.path("origin").asString())
        assertEquals(keyPath, ca.path("keyPath").asString())
        assertEquals(true, state.json.path("caReplaceable").asBoolean())
    }

    @Test
    fun `Администратор после мастера видит выполненные шаги и CA`() {
        val admin = client.completeWizard()

        val state = client.state(admin = admin)

        assertEquals(listOf("done", "done", "upcoming", "upcoming"), stepStates(state))
        assertEquals("not_issued", state.json.path("setupCode").asString())
        assertEquals("admin", state.json.path("access").asString())
        assertEquals(false, state.json.path("caReplaceable").asBoolean())
    }

    @Test
    fun `Отпечаток шага CA равен отпечатку из сведений о CA сервера`() {
        val session = client.setupSession()
        val remembered = client.state(session).json.path("ca")
        assertEquals(204, client.confirmCa(session).status)
        val admin = checkNotNull(client.admin(session, OWNER_PASSWORD).cookie(SESSION_COOKIE))

        val info = client.send("GET", "/api/v1/ca", cookies = mapOf(SESSION_COOKIE to admin))

        assertEquals(200, info.status)
        assertEquals(remembered, info.json)
    }

    @Test
    fun `Сведения о CA сервера содержат происхождение и путь ключа`() {
        val admin = client.completeWizard()

        val info = client.send("GET", "/api/v1/ca", cookies = mapOf(SESSION_COOKIE to admin))

        assertEquals(setOf("fingerprint", "origin", "keyPath"), info.json.propertyNames().toSet())
        assertEquals("generated", info.json.path("origin").asString())
        assertEquals(keyPath, info.json.path("keyPath").asString())
    }

    @Test
    fun `Признак заменяемости CA следует условиям замены`() {
        val session = client.setupSession()
        assertEquals(
            true,
            client
                .state(session)
                .json
                .path("caReplaceable")
                .asBoolean(),
        )

        val agent = UUID.randomUUID()
        jdbc.update("insert into agents (id, tenant_id, hostname, registered_at) values (?, ?, 'h', now())", agent, tenantOfDefault())
        jdbc.update(
            "insert into agent_certificates (serial, tenant_id, agent_id, issued_at, not_after) values (?, ?, ?, now(), now() + interval '1 day')",
            "c".repeat(32),
            tenantOfDefault(),
            agent,
        )
        try {
            assertEquals(
                false,
                client
                    .state(session)
                    .json
                    .path("caReplaceable")
                    .asBoolean(),
            )
        } finally {
            jdbc.update("delete from agent_certificates where serial = ?", "c".repeat(32))
            jdbc.update("delete from agents where id = ?", agent)
        }

        assertEquals(204, client.confirmCa(session).status)
        assertEquals(
            false,
            client
                .state(session)
                .json
                .path("caReplaceable")
                .asBoolean(),
        )
    }

    private fun tenantOfDefault() = UUID.fromString("00000000-0000-0000-0000-000000000001")

    // ---- Правило: Шаг ca выполняется явным подтверждением CA с сессией настройки ----

    @Test
    fun `Подтверждение CA с сессией настройки выполняет шаг ca`() {
        val session = client.setupSession()

        val reply = client.confirmCa(session)

        assertEquals(204, reply.status)
        assertEquals("", reply.body)
        assertEquals(
            "done",
            client
                .state(session)
                .json
                .path("steps")
                .path(0)
                .path("state")
                .asString(),
        )
        assertEquals(
            false,
            client
                .state(session)
                .json
                .path("caReplaceable")
                .asBoolean(),
        )
    }

    @Test
    fun `Повторное подтверждение CA ничего не меняет`() {
        val session = client.setupSession()
        client.confirmCa(session)

        assertEquals(204, client.confirmCa(session).status)

        assertEquals(listOf("done", "pending"), stepStates(client.state(session)).take(2))
    }

    @Test
    fun `Подтверждение CA не выполняет шаг admin и не выдаёт сессию администратора`() {
        val session = client.setupSession()

        val reply = client.confirmCa(session)

        assertNull(reply.setCookie(SESSION_COOKIE))
        assertEquals("pending", stepStates(client.state(session))[1])
        assertEquals(
            "setup",
            client
                .state(session)
                .json
                .path("access")
                .asString(),
        )
    }

    @Test
    fun `Подтверждение CA с чужим Origin не выполняет шаг`() {
        val session = client.setupSession()

        val reply = client.confirmCa(session, headers = mapOf("Origin" to EVIL))

        assertEquals(403, reply.status)
        assertEquals("origin_rejected", reply.code)
        assertEquals("pending", stepStates(client.state(session))[0])
    }

    @Test
    fun `Сессия администратора без сессии настройки не выполняет шаг ca`() {
        JdbcAdministrators(jdbc).create(PasswordHasher().hash(OWNER_PASSWORD), clock.instant())
        val admin = checkNotNull(client.login(OWNER_PASSWORD).cookie(SESSION_COOKIE))

        val reply = client.send("POST", "/api/v1/onboarding/ca", cookies = mapOf(SESSION_COOKIE to admin))

        assertEquals(401, reply.status)
        assertEquals("pending", stepStates(client.state(admin = admin))[0])
    }

    @Test
    fun `Шаг ca выполняется в состоянии установки, а не тенанта`() {
        jdbc.update("insert into tenants (id, name) values (?, 'B') on conflict do nothing", TENANT_B)
        try {
            val session = client.setupSession()
            client.confirmCa(session)
            client.confirmCa(session)

            assertEquals(1, jdbc.queryForObject("select count(*) from onboarding_steps where step = 'ca'", Int::class.java))
        } finally {
            jdbc.update("delete from tenants where id = ?", TENANT_B)
        }
    }

    // ---- Правило: Шаг admin задаёт пароль и сразу выдаёт сессию администратора ----

    @Test
    fun `Шаг admin отвечает 204 и выдаёт сессию администратора`() {
        val session = setupReady()

        val reply = client.admin(session, OWNER_PASSWORD)

        assertEquals(204, reply.status)
        assertEquals("", reply.body)
        val admin = reply.setCookie(SESSION_COOKIE)!!
        assertTrue("HttpOnly" in admin && "SameSite=Strict" in admin && "Path=/" in admin, admin)
        val cleared = reply.setCookie(SETUP_COOKIE)!!
        assertTrue(cleared.startsWith("$SETUP_COOKIE=;") && "Max-Age=0" in cleared, cleared)
        assertTrue("Path=/api/v1/onboarding" in cleared, cleared)
    }

    @Test
    fun `Сессия из шага admin — обычная сессия администратора`() {
        val admin = client.completeWizard()

        val reply = client.session(admin)

        assertEquals(200, reply.status)
        assertEquals("00000000-0000-0000-0000-000000000001", reply.json.path("tenantId").asString())
        assertEquals("2026-10-10T00:00:00Z", reply.json.path("expiresAt").asString())
    }

    @Test
    fun `После шага admin вход заданным паролем принимается, другим нет`() {
        client.completeWizard()

        assertEquals(401, client.login("Correct-Horse-Battery").status)
        val reply = client.login(OWNER_PASSWORD)
        assertEquals(204, reply.status)
        assertTrue(reply.cookie(SESSION_COOKIE)!!.isNotEmpty())
    }

    @Test
    fun `После шага admin все сессии настройки не действуют`() {
        val s1 = client.setupSession()
        val s2 = client.setupSession()
        client.confirmCa(s1)

        client.admin(s1, OWNER_PASSWORD)

        assertEquals(
            "none",
            client
                .state(s1)
                .json
                .path("access")
                .asString(),
        )
        assertEquals(
            "none",
            client
                .state(s2)
                .json
                .path("access")
                .asString(),
        )
        assertEquals(401, client.admin(s2, "attacker-password-1").status)
        assertEquals(401, client.login("attacker-password-1").status)
    }

    @Test
    fun `Шаг admin до шага ca отклоняется и не сохраняет пароль`() {
        val session = client.setupSession()

        val reply = client.admin(session, OWNER_PASSWORD)

        assertEquals(409, reply.status)
        assertEquals("ca_step_pending", reply.code)
        assertEquals("pending", stepStates(client.state(session))[1])
        assertEquals(
            "setup",
            client
                .state(session)
                .json
                .path("access")
                .asString(),
        )
        assertNull(JdbcAdministrators(jdbc).hash())
    }

    @Test
    fun `Длина пароля в шаге admin считается в кодовых точках от 12 до 1024`() {
        val cases =
            listOf(
                "" to 422,
                "short-pw-11" to 422,
                "парольнекор" to 422,
                "exactly-12ch" to 204,
                "паролькирилл" to 204,
                "🔑".repeat(12) to 204,
                "a".repeat(1024) to 204,
                "a".repeat(1025) to 422,
            )
        for ((password, status) in cases) {
            installation.restore()
            val session = setupReady()

            val reply = client.admin(session, password)

            assertEquals(status, reply.status, password.take(20))
            if (status == 422) {
                assertEquals("validation_failed", reply.code)
                assertEquals(
                    listOf("password"),
                    reply.json
                        .path("errors")
                        .list()
                        .map { it.path("field").asString() },
                )
            } else {
                assertTrue(reply.cookie(SESSION_COOKIE)!!.isNotEmpty())
            }
        }
    }

    @Test
    fun `Шаг admin без пароля отклоняется как недопустимый пароль`() {
        val session = setupReady()
        for (body in listOf("{}", """{"password": null}""", "password=x")) {
            val reply = client.adminRaw(body, session)

            assertEquals(422, reply.status, body)
            assertEquals("validation_failed", reply.code, body)
            assertEquals(
                listOf("password"),
                reply.json
                    .path("errors")
                    .list()
                    .map { it.path("field").asString() },
                body,
            )
        }
    }

    @Test
    fun `Отказ по длине пароля не завершает сессию настройки`() {
        val session = setupReady()
        assertEquals(422, client.admin(session, "short-pw-11").status)

        val reply = client.admin(session, OWNER_PASSWORD)

        assertEquals(204, reply.status)
        assertTrue(reply.cookie(SESSION_COOKIE)!!.isNotEmpty())
    }

    @Test
    fun `Пароль из шага admin не обрезается`() {
        client.completeWizard("correct-horse-battery ")

        assertEquals(401, client.login("correct-horse-battery").status)
        assertEquals(204, client.login("correct-horse-battery ").status)
    }

    @Test
    fun `Шаг admin с чужим Origin не задаёт пароль`() {
        val session = setupReady()

        val reply = client.admin(session, OWNER_PASSWORD, headers = mapOf("Origin" to EVIL))

        assertEquals(403, reply.status)
        assertEquals("origin_rejected", reply.code)
        assertEquals("pending", stepStates(client.state(session))[1])
    }

    @Test
    fun `Одновременные шаги admin двух сессий настройки задают ровно один пароль`() {
        val s1 = client.setupSession()
        val s2 = client.setupSession()
        client.confirmCa(s1)
        val start = CountDownLatch(1)
        val pool = Executors.newFixedThreadPool(2)
        val first =
            pool.submit<Reply> {
                start.await()
                client.admin(s1, "first-password-01")
            }
        val second =
            pool.submit<Reply> {
                start.await()
                client.admin(s2, "second-password-2")
            }
        start.countDown()
        val replies = listOf(first.get(30, TimeUnit.SECONDS), second.get(30, TimeUnit.SECONDS))
        pool.shutdown()

        val winners = replies.filter { it.status == 204 }
        assertEquals(1, winners.size, "$replies")
        assertTrue(winners.single().cookie(SESSION_COOKIE)!!.isNotEmpty())
        val loser = replies.single { it.status != 204 }
        assertNull(loser.setCookie(SESSION_COOKIE))
        val winning = if (replies[0].status == 204) "first-password-01" else "second-password-2"
        val losing = if (replies[0].status == 204) "second-password-2" else "first-password-01"
        assertEquals(204, client.login(winning).status)
        assertEquals(401, client.login(losing).status)
    }

    // ---- Правило: Пароль хранится только как хэш Argon2id ----

    @Test
    fun `После шага admin база хранит хэш Argon2id с заданными параметрами`() {
        client.completeWizard()

        val hash = jdbc.queryForObject("select password_hash from administrators", String::class.java)!!

        assertTrue(hash.startsWith("\$argon2id\$v=19\$m=19456,t=2,p=1\$"), hash)
    }

    @Test
    fun `База не содержит пароль администратора`() {
        client.completeWizard()

        val tables = jdbc.queryForList("select table_name from information_schema.tables where table_schema = 'public'", String::class.java)
        for (table in tables) {
            val rows = jdbc.queryForList("select t::text from \"$table\" t", String::class.java)
            assertTrue(rows.none { OWNER_PASSWORD in it.orEmpty() }, "the password is in $table")
        }
    }

    // ---- Правило: Вход до создания администратора отвечает 409 setup_required ----

    @Test
    fun `Вход до шага admin отвечает 409 setup_required без cookie`() {
        val reply = client.login(OWNER_PASSWORD)

        assertEquals(409, reply.status)
        assertEquals("setup_required", reply.code)
        assertEquals("application/problem+json", reply.contentType)
        assertTrue(reply.setCookies().isEmpty())
    }

    @Test
    fun `Ответ setup_required не зависит от присланного пароля`() {
        val reference = client.login("wrong-password-123")

        assertTrue(client.login("").sameAs(reference))
    }

    @Test
    fun `Вход до шага admin не засчитывается в перебор пароля`() {
        repeat(10) { assertEquals(409, client.login("wrong-password-123").status) }
        client.completeWizard()

        assertEquals(401, client.login("wrong-password-123").status)
    }

    @Test
    fun `Вход с чужим Origin до шага admin отклоняется по Origin`() {
        val reply = client.login(OWNER_PASSWORD, headers = mapOf("Origin" to EVIL))

        assertEquals(403, reply.status)
        assertEquals("origin_rejected", reply.code)
    }

    // ---- Правило: До шага admin открыты только статус, состояние онбординга и ввод кода ----

    @Test
    fun `Операция администратора до шага admin без cookie отвечает 401`() {
        val id = "0192f7a0-0000-7000-8000-000000000201"
        val operations =
            listOf(
                "GET" to "/api/v1/session",
                "DELETE" to "/api/v1/session",
                "PUT" to "/api/v1/session/password",
                "GET" to "/api/v1/ca",
                "GET" to "/api/v1/agents",
                "GET" to "/api/v1/overview",
                "GET" to "/api/v1/enrollment-tokens",
                "POST" to "/api/v1/enrollment-tokens",
                "GET" to "/api/v1/sources",
                "GET" to "/api/v1/runs",
                "GET" to "/api/v1/sources/$id",
            )
        for ((method, path) in operations) {
            val reply = client.send(method, path, body = if (method == "GET") null else "{}")
            assertEquals(401, reply.status, "$method $path")
            assertEquals("unauthenticated", reply.code, "$method $path")
        }
    }

    @Test
    fun `Статус сервера открыт до шага admin`() {
        val reply = client.send("GET", "/api/v1/status")

        assertEquals(200, reply.status)
        assertTrue(reply.json.has("version"))
    }

    @Test
    fun `Состояние администратора не зависит от смены часов — сессия настройки ждёт срока кода`() {
        val session = setupReady()
        clock.now = T0 + Duration.ofHours(23)

        assertEquals(204, client.admin(session, OWNER_PASSWORD).status)
        assertNotEquals(0, jdbc.queryForObject("select count(*) from administrators", Int::class.java))
    }
}
