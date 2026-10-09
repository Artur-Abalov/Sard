// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.onboarding

import dev.sard.server.api.SESSION_COOKIE
import dev.sard.server.api.SETUP_COOKIE
import dev.sard.server.auth.LoginAttemptTracker
import dev.sard.server.auth.SessionStore
import dev.sard.server.pki.CertificateAuthority
import dev.sard.server.pki.MovableClock
import dev.sard.server.selfagent.captureEvents
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.test.web.server.LocalServerPort
import org.springframework.jdbc.core.JdbcTemplate
import tools.jackson.databind.ObjectMapper
import kotlin.test.BeforeTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

private const val CLIENT = "203.0.113.10"
private const val OTHER = "198.51.100.7"

private fun from(address: String) = mapOf("X-Forwarded-For" to address)

/**
 * The scenarios of docs/specs/server/onboarding-setup.feature that need the address of the client to be a chosen
 * one: the server trusts X-Forwarded-For of a proxy on the loopback address (SARD_FORWARD_HEADERS=native, ADR 0046).
 */
@FirstStartBehindProxyTest
class FirstStartBehindProxyIntegrationTest(
    @Autowired jdbc: JdbcTemplate,
    @Autowired clock: MovableClock,
    @Autowired codes: SetupCodes,
    @Autowired sessions: SetupSessions,
    @Autowired codeAttempts: SetupCodeAttempts,
    @Autowired adminSessions: SessionStore,
    @Autowired signInAttempts: LoginAttemptTracker,
    @Autowired private val ca: CertificateAuthority,
    @Autowired mapper: ObjectMapper,
    @LocalServerPort port: Int,
) {
    private val client = FirstStartClient(port, mapper)
    private val installation = CleanInstallation(jdbc, clock, codes, sessions, codeAttempts, adminSessions, signInAttempts)

    @BeforeTest
    fun `a clean installation with the code C`() = installation.restore()

    private fun wrongCodes(
        times: Int,
        address: String = CLIENT,
    ) = repeat(times) { client.enterCode(WRONG_CODE, headers = from(address)) }

    @Test
    fun `Атрибут Secure cookie сессии настройки следует соединению`() {
        val http = client.enterCode(CODE, headers = from(CLIENT)).setCookie(SETUP_COOKIE)!!
        val https = client.enterCode(CODE, headers = from(CLIENT) + ("X-Forwarded-Proto" to "https")).setCookie(SETUP_COOKIE)!!

        assertFalse("Secure" in http, http)
        assertTrue("Secure" in https, https)
    }

    @Test
    fun `Блокировка ввода кода одного адреса не мешает другому`() {
        wrongCodes(5)

        assertEquals(429, client.enterCode(CODE, headers = from(CLIENT)).status)
        assertEquals(204, client.enterCode(CODE, headers = from(OTHER)).status)
    }

    @Test
    fun `Во время блокировки смена пароля отвечает 429, и пароль не менялся`() {
        val a = client.completeWizard()
        repeat(5) { client.login("wrong-password-123", from(CLIENT)) }

        val reply =
            client.send(
                "PUT",
                "/api/v1/session/password",
                """{"currentPassword":"$OWNER_PASSWORD","newPassword":"new-password-2026"}""",
                cookies = mapOf(SESSION_COOKIE to a),
                headers = from(CLIENT),
            )

        assertEquals(429, reply.status)
        assertEquals("900", reply.header("Retry-After"))
        assertEquals(401, client.login("new-password-2026", from(OTHER)).status)
        assertTrue(client.alive(a))
    }

    @Test
    fun `Ввод кода, шаг ca и шаг admin пишутся в лог с адресом клиента`() {
        val events =
            captureEvents {
                client.enterCode(WRONG_CODE, headers = from(CLIENT))
                val setup = checkNotNull(client.enterCode(CODE, headers = from(CLIENT)).cookie(SETUP_COOKIE))
                client.send("POST", "/api/v1/onboarding/ca", cookies = mapOf(SETUP_COOKIE to setup), headers = from(CLIENT))
                client.send(
                    "POST",
                    "/api/v1/onboarding/admin",
                    """{"password":"$OWNER_PASSWORD"}""",
                    cookies = mapOf(SETUP_COOKIE to setup),
                    headers = from(CLIENT),
                )
            }

        val lines = events.map { it.text }

        fun records(text: String) = lines.filter { text in it && CLIENT in it }
        assertEquals(1, records("Setup code rejected").size, "$lines")
        assertEquals(1, records("Setup code accepted").size, "$lines")
        assertEquals(1, records("Onboarding step ca completed").size, "$lines")
        assertEquals(1, records("Onboarding step admin completed").size, "$lines")
        assertTrue(records("Onboarding step ca completed").single().contains(ca.fingerprint().hex))
    }

    @Test
    fun `Блокировка ввода кода пишется в лог один раз`() {
        wrongCodes(4)

        val events = captureEvents { wrongCodes(4) }

        val locks = events.filter { "Setup code entry locked" in it.text && CLIENT in it.text }
        assertEquals(1, locks.size, "$events")
    }

    @Test
    fun `Смена пароля пишется в лог с адресом клиента и без паролей`() {
        val a = client.completeWizard()
        lateinit var replies: List<Reply>

        val events =
            captureEvents {
                val cookies = mapOf(SESSION_COOKIE to a)
                val wrong =
                    client.send(
                        "PUT",
                        "/api/v1/session/password",
                        """{"currentPassword":"wrong-password-123","newPassword":"new-password-2026"}""",
                        cookies = cookies,
                        headers = from(CLIENT),
                    )
                val right =
                    client.send(
                        "PUT",
                        "/api/v1/session/password",
                        """{"currentPassword":"$OWNER_PASSWORD","newPassword":"new-password-2026"}""",
                        cookies = cookies,
                        headers = from(CLIENT),
                    )
                replies = listOf(wrong, right)
            }

        val lines = events.map { it.text }
        assertEquals(1, lines.count { "Password change failed" in it && CLIENT in it }, "$lines")
        assertEquals(1, lines.count { "Password changed" in it && CLIENT in it }, "$lines")
        for (secret in listOf(OWNER_PASSWORD, "wrong-password-123", "new-password-2026")) {
            assertTrue(lines.none { secret in it }, "$secret is in the log")
            assertTrue(replies.none { secret in it.everything() }, "$secret is in a response")
        }
    }
}
