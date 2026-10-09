// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.onboarding

import dev.sard.server.api.SESSION_COOKIE
import dev.sard.server.api.list
import dev.sard.server.auth.LoginAttemptTracker
import dev.sard.server.auth.SessionStore
import dev.sard.server.pki.MovableClock
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.test.web.server.LocalServerPort
import org.springframework.jdbc.core.JdbcTemplate
import tools.jackson.databind.ObjectMapper
import java.util.concurrent.CountDownLatch
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit
import kotlin.test.BeforeTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNotEquals
import kotlin.test.assertTrue

private const val NEW_PASSWORD = "new-password-2026"
private const val EVIL = "https://evil.example"

/** Rule "Администратор меняет пароль из консоли, прочие сессии завершаются" of docs/specs/server/onboarding-setup.feature. */
@FirstStartTest
class PasswordChangeIntegrationTest(
    @Autowired private val jdbc: JdbcTemplate,
    @Autowired clock: MovableClock,
    @Autowired codes: SetupCodes,
    @Autowired sessions: SetupSessions,
    @Autowired codeAttempts: SetupCodeAttempts,
    @Autowired adminSessions: SessionStore,
    @Autowired signInAttempts: LoginAttemptTracker,
    @Autowired mapper: ObjectMapper,
    @LocalServerPort port: Int,
) {
    private val client = FirstStartClient(port, mapper)
    private val installation = CleanInstallation(jdbc, clock, codes, sessions, codeAttempts, adminSessions, signInAttempts)
    private lateinit var a: String

    @BeforeTest
    fun `the owner went through the wizard and holds the session A`() {
        installation.restore()
        a = client.completeWizard()
    }

    private fun adminCookie(admin: String) = mapOf(SESSION_COOKIE to admin)

    @Test
    fun `Смена пароля с верным текущим отвечает 204`() {
        val reply = client.changePassword(a, OWNER_PASSWORD, NEW_PASSWORD)

        assertEquals(204, reply.status)
        assertEquals("", reply.body)
    }

    @Test
    fun `После смены пароля новый пароль принимается, а прежний нет`() {
        client.changePassword(a, OWNER_PASSWORD, NEW_PASSWORD)

        assertEquals(401, client.login(OWNER_PASSWORD).status)
        val reply = client.login(NEW_PASSWORD)
        assertEquals(204, reply.status)
        assertTrue(reply.cookie(SESSION_COOKIE)!!.isNotEmpty())
    }

    @Test
    fun `Смена пароля завершает все прочие сессии администратора`() {
        val b1 = checkNotNull(client.login(OWNER_PASSWORD).cookie(SESSION_COOKIE))
        val b2 = checkNotNull(client.login(OWNER_PASSWORD).cookie(SESSION_COOKIE))

        client.changePassword(a, OWNER_PASSWORD, NEW_PASSWORD)

        assertFalse(client.alive(b1))
        assertFalse(client.alive(b2))
    }

    @Test
    fun `Смена пароля продолжает текущую сессию под новым идентификатором`() {
        val reply = client.changePassword(a, OWNER_PASSWORD, NEW_PASSWORD)

        val cookie = reply.setCookie(SESSION_COOKIE)!!
        assertTrue("HttpOnly" in cookie && "SameSite=Strict" in cookie && "Path=/" in cookie, cookie)
        val a2 = reply.cookie(SESSION_COOKIE)!!
        assertNotEquals(a, a2)
        assertTrue(client.alive(a2))
        assertFalse(client.alive(a))
    }

    @Test
    fun `Неверный текущий пароль отвечает 422 wrong_password и ничего не меняет`() {
        val b = checkNotNull(client.login(OWNER_PASSWORD).cookie(SESSION_COOKIE))

        val reply = client.changePassword(a, "wrong-password-123", NEW_PASSWORD)

        assertEquals(422, reply.status)
        assertEquals("wrong_password", reply.code)
        assertEquals("application/problem+json", reply.contentType)
        assertTrue(client.alive(a) && client.alive(b))
        assertEquals(204, client.login(OWNER_PASSWORD).status)
    }

    @Test
    fun `Неверный текущий пароль засчитывается в перебор пароля`() {
        repeat(4) { client.login("wrong-password-123") }
        client.changePassword(a, "wrong-password-123", NEW_PASSWORD)

        assertEquals(429, client.login(OWNER_PASSWORD).status)
    }

    @Test
    fun `Во время блокировки смена пароля отвечает 429 и не проверяет пароль`() {
        repeat(5) { client.login("wrong-password-123") }

        val reply = client.changePassword(a, OWNER_PASSWORD, NEW_PASSWORD)

        assertEquals(429, reply.status)
        assertEquals("too_many_attempts", reply.code)
        assertEquals("900", reply.header("Retry-After"))
        assertTrue(client.alive(a))
    }

    @Test
    fun `Недопустимый новый пароль отвечает 422 validation_failed и не засчитывается`() {
        repeat(4) { client.login("wrong-password-123") }
        for (bad in listOf("", "short-pw-11", "a".repeat(1025))) {
            val reply = client.changePassword(a, OWNER_PASSWORD, bad)

            assertEquals(422, reply.status)
            assertEquals("validation_failed", reply.code)
            assertEquals(
                listOf("newPassword"),
                reply.json
                    .path("errors")
                    .list()
                    .map { it.path("field").asString() },
            )
        }

        assertEquals(204, client.login(OWNER_PASSWORD).status)
    }

    @Test
    fun `Тело смены пароля без полей отвечает 422 validation_failed`() {
        val bodies =
            listOf(
                "{}",
                """{"currentPassword": "$OWNER_PASSWORD"}""",
                """{"newPassword": "$NEW_PASSWORD"}""",
                "x=y",
            )
        for (body in bodies) {
            val reply = client.changeRaw(body, a)

            assertEquals(422, reply.status, body)
            assertEquals("validation_failed", reply.code, body)
        }

        assertEquals(204, client.login(OWNER_PASSWORD).status)
    }

    @Test
    fun `Смена пароля без сессии отвечает 401`() {
        val reply = client.changePassword(null, OWNER_PASSWORD, NEW_PASSWORD)

        assertEquals(401, reply.status)
        assertEquals("unauthenticated", reply.code)
        assertEquals(204, client.login(OWNER_PASSWORD).status)
    }

    @Test
    fun `Смена пароля с чужим Origin не меняет пароль`() {
        val reply = client.changePassword(a, OWNER_PASSWORD, NEW_PASSWORD, headers = mapOf("Origin" to EVIL))

        assertEquals(403, reply.status)
        assertEquals("origin_rejected", reply.code)
        assertEquals(204, client.login(OWNER_PASSWORD).status)
    }

    @Test
    fun `Одновременные смены пароля из двух сессий оставляют один пароль`() {
        val b = checkNotNull(client.login(OWNER_PASSWORD).cookie(SESSION_COOKIE))
        val start = CountDownLatch(1)
        val pool = Executors.newFixedThreadPool(2)
        val fromA =
            pool.submit<Reply> {
                start.await()
                client.changePassword(a, OWNER_PASSWORD, "password-from-a1")
            }
        val fromB =
            pool.submit<Reply> {
                start.await()
                client.changePassword(b, OWNER_PASSWORD, "password-from-b1")
            }
        start.countDown()
        val replies = listOf(fromA.get(60, TimeUnit.SECONDS), fromB.get(60, TimeUnit.SECONDS))
        pool.shutdown()

        assertEquals(1, replies.count { it.status == 204 }, "$replies")
        val winning = if (replies[0].status == 204) "password-from-a1" else "password-from-b1"
        val losing = if (replies[0].status == 204) "password-from-b1" else "password-from-a1"
        assertEquals(204, client.login(winning).status)
        assertEquals(401, client.login(losing).status)
    }

    @Test
    fun `В базе после смены хэш нового пароля с теми же параметрами, а не текст`() {
        val before = jdbc.queryForObject("select password_hash from administrators", String::class.java)!!

        client.changePassword(a, OWNER_PASSWORD, NEW_PASSWORD)

        val after = jdbc.queryForObject("select password_hash from administrators", String::class.java)!!
        assertNotEquals(before, after)
        assertTrue(after.startsWith("\$argon2id\$v=19\$m=19456,t=2,p=1\$"), after)
        assertFalse(NEW_PASSWORD in after)
    }
}
