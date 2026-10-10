// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.onboarding

import dev.sard.server.api.NoSuchSessionException
import dev.sard.server.api.SESSION_COOKIE
import dev.sard.server.api.SESSION_REQUEST_ATTRIBUTE
import dev.sard.server.api.SETUP_COOKIE
import dev.sard.server.api.Session
import dev.sard.server.api.SessionApi
import dev.sard.server.api.SignInResult
import jakarta.servlet.FilterChain
import jakarta.servlet.http.HttpServletRequest
import jakarta.servlet.http.HttpServletResponse
import org.junit.jupiter.api.io.TempDir
import org.springframework.boot.web.servlet.FilterRegistrationBean
import org.springframework.web.filter.OncePerRequestFilter
import java.io.ByteArrayOutputStream
import java.io.PrintStream
import java.nio.file.Path
import java.time.Duration
import kotlin.test.AfterTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNotEquals
import kotlin.test.assertNull
import kotlin.test.assertTrue

private const val CODE2 = "WXYZ-0123-4567-89AB-CDEF-GHJK-MNPQ"
private val CODE_LINE = Regex("SARD SETUP CODE: ([0-9A-HJKMNP-TV-Z]{4}(-[0-9A-HJKMNP-TV-Z]{4}){6}) valid until (\\S+)")

/**
 * The scenarios of docs/specs/server/onboarding-setup.feature that need a server to start again: the code at
 * start, what a restart ends and keeps, admin-reset, the environment variable that is no more, an extension that
 * signs people in. Each test runs real servers, one after another, over one CA directory and one database.
 */
class FirstStartRestartIntegrationTest {
    @TempDir
    lateinit var tmp: Path

    private val running = mutableListOf<RunningServer>()
    private val installation by lazy { Installation(tmp) }

    @AfterTest
    fun `stop every server`() {
        running.forEach { runCatching { it.close() } }
    }

    private fun start(options: StartOptions = StartOptions()): RunningServer {
        val server = installation.start(options)
        running += server
        return server
    }

    private fun restart(
        server: RunningServer,
        options: StartOptions = StartOptions(),
    ): RunningServer {
        server.close()
        running -= server
        return start(options)
    }

    private fun codeLines(server: RunningServer) = server.startLog.filter { CODE_LINE.containsMatchIn(it.text) }

    // ---- Правило: Сервер без администратора печатает код настройки при старте ----

    @Test
    fun `Первый старт чистой установки печатает код настройки`() {
        val server = start()

        val lines = codeLines(server)
        assertEquals(1, lines.size, server.logText)
        val match = CODE_LINE.find(lines.single().text)!!
        assertEquals(CODE, match.groupValues[1])
        assertEquals("2026-10-10T12:00:00Z", match.groupValues[3])
        assertEquals(ch.qos.logback.classic.Level.INFO, lines.single().level)
    }

    @Test
    fun `Блок кода называет страницу мастера и команду чтения лога`() {
        val server = start()

        val index = server.startLog.indexOfFirst { CODE_LINE.containsMatchIn(it.text) }
        val around =
            server.startLog
                .subList(index - 1, index + 2)
                .filterIndexed { i, _ -> i != 1 }
                .map { it.text }
        assertTrue(around.any { "/setup" in it }, "$around")
        assertTrue(around.any { "docker compose logs server" in it }, "$around")
        assertTrue(around.none { CODE in it || CODE.replace("-", "") in it }, "$around")
    }

    @Test
    fun `Старт с выполненным шагом admin не печатает код`() {
        val first = start()
        first.client.completeWizard("correct-horse-battery")

        val second = restart(first)

        assertTrue(codeLines(second).isEmpty(), second.logText)
        assertEquals(
            "not_issued",
            second.client
                .state()
                .json
                .path("setupCode")
                .asString(),
        )
    }

    @Test
    fun `Перезапуск до шага admin печатает новый код, и прежний код недействителен`() {
        val first = start(StartOptions(codes = listOf(CODE)))
        assertEquals(204, first.client.enterCode(CODE).status)

        val second = restart(first, StartOptions(codes = listOf(CODE2)))

        val match = CODE_LINE.find(codeLines(second).single().text)!!
        assertEquals(CODE2, match.groupValues[1])
        assertNotEquals(CODE, match.groupValues[1])
        assertEquals(401, second.client.enterCode(CODE).status)
        assertEquals(204, second.client.enterCode(CODE2).status)
    }

    @Test
    fun `Перезапуск сервера завершает сессии настройки`() {
        val first = start()
        val session = first.client.setupSession()

        val second = restart(first)

        assertEquals(401, second.client.confirmCa(session).status)
        assertEquals(
            "pending",
            second.client
                .state()
                .json
                .path("steps")
                .path(0)
                .path("state")
                .asString(),
        )
    }

    @Test
    fun `Перезапуск сервера снимает блокировку ввода кода`() {
        val first = start()
        repeat(5) { first.client.enterCode(WRONG_CODE) }
        assertEquals(429, first.client.enterCode(CODE).status)

        val second = restart(first, StartOptions(codes = listOf(CODE2)))

        assertEquals(204, second.client.enterCode(CODE2).status)
    }

    @Test
    fun `Пароль переживает перезапуск сервера, и старт не печатает код`() {
        val first = start()
        first.client.completeWizard("correct-horse-battery")

        val second = restart(first)

        val reply = second.client.login("correct-horse-battery")
        assertEquals(204, reply.status)
        assertTrue(reply.cookie(SESSION_COOKIE)!!.isNotEmpty())
        assertTrue(codeLines(second).isEmpty())
    }

    @Test
    fun `Новый пароль переживает перезапуск сервера`() {
        val first = start()
        val admin = first.client.completeWizard("correct-horse-battery")
        assertEquals(204, first.client.changePassword(admin, "correct-horse-battery", "new-password-2026").status)

        val second = restart(first)

        assertEquals(204, second.client.login("new-password-2026").status)
        assertEquals(401, second.client.login("correct-horse-battery").status)
        assertTrue(codeLines(second).isEmpty(), second.logText)
    }

    // ---- Правило: Команда admin-reset возвращает доступ только тому, у кого есть хост сервера ----

    private fun adminReset(): Int {
        val out = ByteArrayOutputStream()
        val environment = installation.database.environment()
        val outcome = ServerCommand().run(arrayOf("admin-reset"), environment, PrintStream(out), PrintStream(out))
        return checkNotNull(outcome)
    }

    @Test
    fun `Работающий сервер после admin-reset отвечает на вход 409 до перезапуска, выданные сессии действуют`() {
        val server = start()
        val admin = server.client.completeWizard("correct-horse-battery")

        assertEquals(0, adminReset())

        val reply = server.client.login("correct-horse-battery")
        assertEquals(409, reply.status)
        assertEquals("setup_required", reply.code)
        assertTrue(server.client.alive(admin))
        val state = server.client.state()
        assertEquals(
            "pending",
            state.json
                .path("steps")
                .path(1)
                .path("state")
                .asString(),
        )
        assertEquals("not_issued", state.json.path("setupCode").asString())
    }

    @Test
    fun `После admin-reset и перезапуска сервер печатает новый код, мастер начинается с шага admin`() {
        val first = start()
        val admin = first.client.completeWizard("correct-horse-battery")
        assertEquals(0, adminReset())

        val second = restart(first, StartOptions(codes = listOf(CODE2)))

        assertEquals(1, codeLines(second).size, second.logText)
        val state = second.client.state()
        assertEquals(
            listOf("done", "pending"),
            state.json
                .path("steps")
                .take(2)
                .map { it.path("state").asString() },
        )
        assertEquals("active", state.json.path("setupCode").asString())
        assertEquals(401, second.client.session(admin).status)
    }

    @Test
    fun `После admin-reset и перезапуска мастер задаёт новый пароль`() {
        val first = start()
        first.client.completeWizard("correct-horse-battery")
        assertEquals(0, adminReset())
        val second = restart(first, StartOptions(codes = listOf(CODE2)))

        val session = second.client.setupSession(CODE2)
        val done = second.client.admin(session, "recovered-password-1")

        assertEquals(204, done.status)
        assertTrue(done.cookie(SESSION_COOKIE)!!.isNotEmpty())
        assertEquals(401, second.client.login("correct-horse-battery").status)
        assertEquals(204, second.client.login("recovered-password-1").status)
    }

    // ---- Правило: Переменной SARD_ADMIN_PASSWORD больше нет ----

    @Test
    fun `Переменная SARD_ADMIN_PASSWORD в окружении не становится паролем`() {
        val first = start()
        first.client.completeWizard("correct-horse-battery")

        val second = restart(first, StartOptions(properties = mapOf("SARD_ADMIN_PASSWORD" to "old-env-password-1")))

        assertEquals(401, second.client.login("old-env-password-1").status)
        assertEquals(204, second.client.login("correct-horse-battery").status)
        val mentioned = "SARD_ADMIN_PASSWORD" in second.logText || "old-env-password-1" in second.logText
        assertFalse(mentioned, "the variable is in the log")
    }

    @Test
    fun `Сервер без пароля в окружении стартует, пароль не нужен`() {
        val server = start()

        assertEquals(409, server.client.login("correct-horse-battery").status)
    }

    // ---- Правило: Онбординг администратора уступает enterprise-замене входа ----

    private class ExtensionSessions : SessionApi {
        override fun createSession(
            password: String,
            clientAddress: String,
            previousSessionId: String?,
        ) = SignInResult.SignedIn("extension-session")

        override fun getSession(sessionId: String): Session = throw NoSuchSessionException()

        override fun deleteSession(
            sessionId: String,
            clientAddress: String,
        ) = Unit
    }

    /** The guard of the extension: whoever sends X-Extension-Admin is its administrator. */
    private class ExtensionGuard : OncePerRequestFilter() {
        override fun doFilterInternal(
            request: HttpServletRequest,
            response: HttpServletResponse,
            chain: FilterChain,
        ) {
            if (request.getHeader("X-Extension-Admin") != null) request.setAttribute(SESSION_REQUEST_ATTRIBUTE, "admin")
            chain.doFilter(request, response)
        }
    }

    private fun extension() =
        StartOptions(
            beans =
                mapOf(
                    "sessionApi" to ExtensionSessions(),
                    "sessionAuthFilterRegistration" to
                        FilterRegistrationBean<jakarta.servlet.Filter>(ExtensionGuard()).apply {
                            urlPatterns = listOf("/api/v1/*")
                            order = 2
                        },
                ),
        )

    @Test
    fun `С заменённым SessionApi код не печатается, шаг admin выполнен`() {
        val server = start(extension())

        assertTrue(codeLines(server).isEmpty(), server.logText)
        val state = server.client.state()
        assertEquals(
            "done",
            state.json
                .path("steps")
                .path(1)
                .path("state")
                .asString(),
        )
        assertEquals("not_issued", state.json.path("setupCode").asString())
    }

    @Test
    fun `С заменённым SessionApi ввод кода и шаг admin отвечают 409 setup_completed`() {
        val server = start(extension())

        val entered = server.client.enterCode(CODE)
        assertEquals(409, entered.status)
        assertEquals("setup_completed", entered.code)
        val admin = server.client.admin("anything", "correct-horse-battery")
        assertEquals(409, admin.status)
        assertEquals("setup_completed", admin.code)
        assertNull(entered.setCookie(SETUP_COOKIE))
    }

    @Test
    fun `С заменённым SessionApi смена пароля отвечает 501, а шаг ca выполняет администратор расширения`() {
        val server = start(extension())

        val change = server.client.changePassword("anything", "a", "b")
        assertEquals(501, change.status)
        assertEquals("not_implemented", change.code)

        assertEquals(401, server.client.confirmCa(null).status)
        val confirmed = server.client.send("POST", "/api/v1/onboarding/ca", headers = mapOf("X-Extension-Admin" to "1"))
        assertEquals(204, confirmed.status)
        val state = server.client.send("GET", "/api/v1/onboarding", headers = mapOf("X-Extension-Admin" to "1"))
        assertEquals("admin", state.json.path("access").asString())
        assertEquals(
            "done",
            state.json
                .path("steps")
                .path(0)
                .path("state")
                .asString(),
        )
    }

    @Test
    fun `Часы сервера в тестах управляемы — срок кода считается от старта`() {
        val server = start()
        server.clock.now = T0 + Duration.ofHours(24)

        assertEquals(401, server.client.enterCode(CODE).status)
        assertEquals(
            "expired",
            server.client
                .state()
                .json
                .path("setupCode")
                .asString(),
        )
    }
}
