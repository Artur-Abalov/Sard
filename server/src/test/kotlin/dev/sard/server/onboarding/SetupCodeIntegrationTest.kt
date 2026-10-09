// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.onboarding

import dev.sard.server.api.SESSION_COOKIE
import dev.sard.server.api.SETUP_COOKIE
import dev.sard.server.auth.LoginAttemptTracker
import dev.sard.server.auth.SessionStore
import dev.sard.server.pki.MovableClock
import dev.sard.server.selfagent.captureEvents
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.test.web.server.LocalServerPort
import org.springframework.jdbc.core.JdbcTemplate
import tools.jackson.databind.ObjectMapper
import java.time.Duration
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

/**
 * Rules "Код действует до шага admin, но не дольше 24 часов", "Неверный код отклоняется одним ответом, перебор
 * блокируется" and "Верный код выдаёт сессию настройки, и она открывает только мастер" of
 * docs/specs/server/onboarding-setup.feature (@http).
 */
@FirstStartTest
class SetupCodeIntegrationTest(
    @Autowired jdbc: JdbcTemplate,
    @Autowired private val clock: MovableClock,
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

    @BeforeTest
    fun `a clean installation with the code C`() = installation.restore()

    private fun wrongCodes(times: Int) = repeat(times) { client.enterCode(WRONG_CODE) }

    private fun valid(setup: String) =
        client
            .state(setup)
            .json
            .path("access")
            .asString() == "setup"

    // ---- Правило: Код действует до шага admin, но не дольше 24 часов ----

    @Test
    fun `Код за миллисекунду до 24 часов принимается`() {
        clock.now = T0 + Duration.ofHours(24) - Duration.ofMillis(1)

        val reply = client.enterCode(CODE)

        assertEquals(204, reply.status)
        assertTrue(reply.cookie(SETUP_COOKIE)!!.isNotEmpty())
    }

    @Test
    fun `Код через 24 часа не принимается`() {
        clock.now = T0 + Duration.ofHours(24)

        val reply = client.enterCode(CODE)

        assertEquals(401, reply.status)
        assertEquals("unauthenticated", reply.code)
    }

    @Test
    fun `Истёкший код виден в состоянии онбординга`() {
        clock.now = T0 + Duration.ofHours(24)

        assertEquals(
            "expired",
            client
                .state()
                .json
                .path("setupCode")
                .asString(),
        )
    }

    @Test
    fun `После истечения кода сервер не печатает новый без перезапуска`() {
        clock.now = T0 + Duration.ofDays(2)

        val events = captureEvents { client.enterCode(CODE) }

        assertTrue(events.none { "SARD SETUP CODE" in it.text }, events.toString())
    }

    @Test
    fun `Код можно ввести повторно, пока шаг admin не выполнен`() {
        val s1 = client.setupSession()

        val second = client.enterCode(CODE)

        assertEquals(204, second.status)
        val s2 = second.cookie(SETUP_COOKIE)!!
        assertNotEquals(s1, s2)
        assertTrue(valid(s1) && valid(s2))
    }

    @Test
    fun `После шага admin код отклоняется как использованный`() {
        client.completeWizard()

        val reply = client.enterCode(CODE)

        assertEquals(409, reply.status)
        assertEquals("setup_completed", reply.code)
        assertEquals("application/problem+json", reply.contentType)
        assertNull(reply.setCookie(SETUP_COOKIE))
    }

    @Test
    fun `После шага admin любой код отклоняется одним и тем же ответом`() {
        client.completeWizard()
        val reference = client.enterCode(CODE)

        assertTrue(client.enterCode("0000-0000-0000-0000-0000-0000-0000").sameAs(reference))
    }

    // ---- Правило: Неверный код отклоняется одним ответом, перебор блокируется ----

    @Test
    fun `Неверный код отвечает 401 без cookie`() {
        val reply = client.enterCode(WRONG_CODE)

        assertEquals(401, reply.status)
        assertEquals("unauthenticated", reply.code)
        assertNull(reply.setCookie(SETUP_COOKIE))
    }

    @Test
    fun `Ответ на неверный код не зависит от причины`() {
        val reference = client.enterCode(WRONG_CODE)
        val variants =
            listOf(
                "",
                "ABCD",
                CODE.dropLast(1) + "6",
                CODE.substringBeforeLast('-'),
                "$CODE-0000",
                "UBCD" + CODE.drop(4),
                "A".repeat(10_000),
            )
        for (code in variants) {
            signInAttemptsReset()
            assertTrue(client.enterCode(code).sameAs(reference), code.take(40))
        }
    }

    private fun signInAttemptsReset() = installation.restore()

    @Test
    fun `Запрос кода без кода отвечает как неверный код и засчитывается`() {
        val reference = client.enterCode(WRONG_CODE)
        for (body in listOf("{}", """{"code": null}""", """{"code": 12345}""", "code=x", "")) {
            installation.restore()
            val reply = client.enterRaw(body)
            assertEquals(401, reply.status, body)
            assertTrue(reply.sameAs(reference), body)
            wrongCodes(4)
            assertEquals(429, client.enterCode(WRONG_CODE).status, "the attempt with <$body> was not counted")
        }
    }

    @Test
    fun `Код принимается в любом из допустимых написаний`() {
        val spellings =
            listOf(
                "abcd-efgh-jkmn-pqrs-tvwx-yz01-2345",
                "ABCDEFGHJKMNPQRSTVWXYZ012345",
                "ABCD EFGH JKMN PQRS TVWX YZ01 2345",
                "  ABCD-EFGH-JKMN-PQRS-TVWX-YZ01-2345\n",
                "ABCD-EFGH-JKMN-PQRS-TVWX-YZO1-2345",
                "ABCD-EFGH-JKMN-PQRS-TVWX-YZ0I-2345",
                "ABCD-EFGH-JKMN-PQRS-TVWX-YZ0l-2345",
            )
        for (spelling in spellings) {
            val reply = client.enterCode(spelling)
            assertEquals(204, reply.status, spelling)
            assertTrue(reply.cookie(SETUP_COOKIE)!!.isNotEmpty(), spelling)
        }
    }

    @Test
    fun `Пятая неудача ввода кода ещё отвечает 401`() {
        wrongCodes(4)

        assertEquals(401, client.enterCode(WRONG_CODE).status)
    }

    @Test
    fun `Шестой ввод кода после пяти неудач отвечает 429 с Retry-After`() {
        wrongCodes(5)

        val reply = client.enterCode(WRONG_CODE)

        assertEquals(429, reply.status)
        assertEquals("too_many_attempts", reply.code)
        assertEquals("900", reply.header("Retry-After"))
    }

    @Test
    fun `Верный код во время блокировки отвечает 429 без сессии настройки`() {
        wrongCodes(5)

        val reply = client.enterCode(CODE)

        assertEquals(429, reply.status)
        assertNull(reply.setCookie(SETUP_COOKIE))
    }

    @Test
    fun `Retry-After ввода кода показывает оставшееся время блокировки`() {
        wrongCodes(5)
        clock.now = T0 + Duration.ofMinutes(10)

        val reply = client.enterCode(CODE)

        assertEquals(429, reply.status)
        assertEquals("300", reply.header("Retry-After"))
    }

    @Test
    fun `Через 15 минут после пятой неудачи верный код принимается`() {
        wrongCodes(5)
        clock.now = T0 + Duration.ofMinutes(15)

        assertEquals(204, client.enterCode(CODE).status)
    }

    @Test
    fun `Успешный ввод кода обнуляет счётчик неудач адреса`() {
        wrongCodes(4)
        client.enterCode(CODE)
        wrongCodes(4)

        assertEquals(401, client.enterCode(WRONG_CODE).status)
    }

    @Test
    fun `Заголовок X-Forwarded-For не обходит блокировку ввода кода`() {
        wrongCodes(5)

        val reply = client.enterCode(CODE, headers = mapOf("X-Forwarded-For" to "192.0.2.99"))

        assertEquals(429, reply.status)
    }

    @Test
    fun `Одновременные неверные коды не обходят порог`() {
        val start = CountDownLatch(1)
        val pool = Executors.newFixedThreadPool(20)
        val statuses =
            (1..20)
                .map {
                    pool.submit<Int> {
                        start.await()
                        client.enterCode(WRONG_CODE).status
                    }
                }.also { start.countDown() }
                .map { it.get(30, TimeUnit.SECONDS) }
        pool.shutdown()

        assertTrue(statuses.count { it == 401 } <= 5, "$statuses")
        assertTrue(statuses.all { it == 401 || it == 429 }, "$statuses")
    }

    @Test
    fun `Попытки входа до создания администратора не засчитываются в перебор кода`() {
        repeat(10) { assertEquals(409, client.login("wrong-password-123").status) }

        assertEquals(401, client.enterCode(WRONG_CODE).status)
    }

    @Test
    fun `Ввод кода с чужим Origin отклоняется и не засчитывается`() {
        wrongCodes(4)
        repeat(3) {
            val reply = client.enterCode(CODE, headers = mapOf("Origin" to EVIL))
            assertEquals(403, reply.status)
            assertEquals("origin_rejected", reply.code)
        }

        assertEquals(401, client.enterCode(WRONG_CODE).status)
    }

    @Test
    fun `Состояние онбординга не меняет счётчик неудач`() {
        wrongCodes(4)
        repeat(20) { client.state() }

        assertEquals(401, client.enterCode(WRONG_CODE).status)
    }

    // ---- Правило: Верный код выдаёт сессию настройки, и она открывает только мастер ----

    @Test
    fun `Верный код отвечает 204 и выдаёт cookie сессии настройки`() {
        val reply = client.enterCode(CODE)

        assertEquals(204, reply.status)
        assertEquals("", reply.body)
        assertTrue(reply.cookie(SETUP_COOKIE)!!.isNotEmpty())
        assertNull(reply.setCookie(SESSION_COOKIE))
    }

    @Test
    fun `Cookie сессии настройки помечена HttpOnly, SameSite Strict и путём мастера`() {
        val cookie = client.enterCode(CODE).setCookie(SETUP_COOKIE)!!

        assertTrue("HttpOnly" in cookie, cookie)
        assertTrue("SameSite=Strict" in cookie, cookie)
        assertTrue("Path=/api/v1/onboarding" in cookie, cookie)
        assertFalse("Max-Age" in cookie || "Expires" in cookie, cookie)
    }

    @Test
    fun `Cookie сессии настройки по HTTP, даже с заголовком X-Forwarded-Proto https, не Secure`() {
        val plain = client.enterCode(CODE).setCookie(SETUP_COOKIE)!!
        val forwarded = client.enterCode(CODE, headers = mapOf("X-Forwarded-Proto" to "https")).setCookie(SETUP_COOKIE)!!

        assertFalse("Secure" in plain, plain)
        assertFalse("Secure" in forwarded, forwarded)
    }

    @Test
    fun `Сессия настройки, присланная до ввода кода, не принимается`() {
        val reply = client.enterCode(CODE, setup = "attacker-chosen")

        assertNotEquals("attacker-chosen", reply.cookie(SETUP_COOKIE))
        assertFalse(valid("attacker-chosen"))
    }

    @Test
    fun `Повторный ввод кода с действующей сессией настройки завершает её`() {
        val s1 = client.setupSession()

        val s2 = client.enterCode(CODE, setup = s1).cookie(SETUP_COOKIE)!!

        assertFalse(valid(s1))
        assertTrue(valid(s2))
    }

    @Test
    fun `Тысяча вводов кода дают тысячу разных сессий настройки`() {
        val ids = (1..1000).map { client.setupSession() }

        assertEquals(1000, ids.toSet().size)
        // hex of 32 random bytes: 256 bits, the floor is 128
        assertTrue(ids.all { it.length >= 32 && it.all { c -> c in "0123456789abcdef" } })
    }

    @Test
    fun `Сессия настройки за миллисекунду до срока кода действует, в срок нет`() {
        val session = client.setupSession()

        clock.now = T0 + Duration.ofHours(24) - Duration.ofMillis(1)
        assertTrue(valid(session))

        clock.now = T0 + Duration.ofHours(24)
        assertFalse(valid(session))
    }

    @Test
    fun `Сессия настройки не открывает операции администратора`() {
        val session = client.setupSession()
        val operations =
            listOf(
                "GET" to "/api/v1/session",
                "GET" to "/api/v1/ca",
                "GET" to "/api/v1/agents",
                "GET" to "/api/v1/overview",
                "POST" to "/api/v1/enrollment-tokens",
                "PUT" to "/api/v1/session/password",
                "DELETE" to "/api/v1/session",
            )
        for ((method, path) in operations) {
            val reply = client.send(method, path, "{}", cookies = mapOf(SETUP_COOKIE to session))
            assertEquals(401, reply.status, "$method $path")
            assertEquals("unauthenticated", reply.code, "$method $path")
        }
    }

    @Test
    fun `Сессия настройки с путём корня тоже не открывает операции администратора`() {
        val session = client.setupSession()

        val reply = client.send("GET", "/api/v1/agents", headers = mapOf("Cookie" to "$SETUP_COOKIE=$session"))

        assertEquals(401, reply.status)
    }

    @Test
    fun `Шаги мастера без сессии настройки отвечают 401, шаги остаются pending`() {
        val requests =
            listOf<(FirstStartClient) -> Reply>(
                { it.confirmCa(null) },
                { it.confirmCa("forged-setup-id") },
                { it.admin(null, "correct-horse-battery") },
                { it.admin("forged-setup-id", "correct-horse-battery") },
            )
        for (request in requests) {
            val reply = request(client)
            assertEquals(401, reply.status)
            assertEquals("unauthenticated", reply.code)
            val steps = client.state().json.path("steps")
            assertEquals(listOf("pending", "pending"), steps.take(2).map { it.path("state").asString() })
        }
    }

    // ---- Правило: Код, пароль и сессии настройки не раскрываются ----

    @Test
    fun `Введённый неверный код не попадает ни в лог, ни в ответ`() {
        val wrong = "WRNG-CODE-ZZZZ-YYYY-XXXX-WWWW-VVVV"
        lateinit var reply: Reply

        val events = captureEvents { reply = client.enterCode(wrong) }

        assertFalse(wrong in reply.everything() || wrong.replace("-", "") in reply.everything())
        assertTrue(events.none { wrong in it.text || wrong.replace("-", "") in it.text }, events.toString())
    }

    @Test
    fun `Ни один ответ сервера не содержит код`() {
        val seen = mutableListOf<String>()
        seen += client.state().everything()
        seen += client.enterCode(WRONG_CODE).everything()
        val entered = client.enterCode(CODE)
        seen += entered.everything()
        val setup = entered.cookie(SETUP_COOKIE)!!
        seen += client.state(setup).everything()
        seen += client.confirmCa(setup).everything()
        seen += client.admin(setup, OWNER_PASSWORD).everything()
        seen += client.send("GET", "/api/v1/status").everything()
        for (path in listOf("/actuator/health", "/actuator/info", "/actuator/env", "/actuator/metrics")) {
            seen += client.send("GET", path).everything()
        }
        for (text in seen) {
            assertFalse(CODE in text || CODE.replace("-", "") in text, text.take(300))
        }
    }

    @Test
    fun `Идентификатор сессии настройки и пароль из шага admin не попадают ни в лог, ни в ответы`() {
        lateinit var setup: String
        lateinit var admin: String
        lateinit var replies: List<Reply>

        val events =
            captureEvents {
                val entered = client.enterCode(CODE)
                setup = entered.cookie(SETUP_COOKIE)!!
                val ca = client.confirmCa(setup)
                val done = client.admin(setup, OWNER_PASSWORD)
                admin = done.cookie(SESSION_COOKIE)!!
                replies = listOf(entered, ca, done)
            }

        val logged = events.joinToString("\n") { it.text }
        assertFalse(setup in logged, "the setup session is in the log")
        assertFalse(admin in logged, "the administrator session is in the log")
        assertFalse(OWNER_PASSWORD in logged, "the password is in the log")
        for (reply in replies) assertFalse(OWNER_PASSWORD in reply.everything())
    }

    @Test
    fun `Эндпоинты actuator с окружением и метриками по-прежнему закрыты`() {
        for (path in listOf("/actuator/env", "/actuator/metrics", "/actuator/heapdump")) {
            assertEquals(404, client.send("GET", path).status, path)
        }
    }
}
