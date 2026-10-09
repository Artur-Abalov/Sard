// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.onboarding

import dev.sard.server.TestcontainersConfiguration
import dev.sard.server.api.SESSION_COOKIE
import dev.sard.server.api.SETUP_COOKIE
import dev.sard.server.auth.LoginAttemptTracker
import dev.sard.server.auth.SessionStore
import dev.sard.server.pki.MovableClock
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.boot.test.context.TestConfiguration
import org.springframework.context.annotation.Bean
import org.springframework.context.annotation.Import
import org.springframework.jdbc.core.JdbcTemplate
import tools.jackson.databind.JsonNode
import tools.jackson.databind.ObjectMapper
import java.net.URI
import java.net.http.HttpClient
import java.net.http.HttpRequest
import java.net.http.HttpResponse
import java.time.Clock

/** The password the owner sets in the wizard in most scenarios. */
const val OWNER_PASSWORD = "correct-horse-battery"

/** A server on random ports with PostgreSQL, the clock at T0 and [CODE] as the setup code: the first start, tested. */
@Target(AnnotationTarget.CLASS)
@Retention(AnnotationRetention.RUNTIME)
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.RANDOM_PORT,
    properties = ["spring.grpc.server.port=0", "sard.agent.stream.check-interval=1h"],
)
@Import(TestcontainersConfiguration::class, FirstStartConfiguration::class)
annotation class FirstStartTest

/** The same behind a proxy that is trusted (ADR 0046): the scheme and the client address come from X-Forwarded-*. */
@Target(AnnotationTarget.CLASS)
@Retention(AnnotationRetention.RUNTIME)
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.RANDOM_PORT,
    properties = ["spring.grpc.server.port=0", "sard.agent.stream.check-interval=1h", "SARD_FORWARD_HEADERS=native"],
)
@Import(TestcontainersConfiguration::class, FirstStartConfiguration::class)
annotation class FirstStartBehindProxyTest

/** The server under test starts at T0 with this setup code. */
@TestConfiguration(proxyBeanMethods = false)
class FirstStartConfiguration {
    @Bean
    fun clock(): Clock = MovableClock(T0)

    @Bean
    fun setupCodeGenerator(): SetupCodeGenerator = SetupCodeGenerator { CODE }

    @Bean
    fun cleanInstallation(
        jdbc: JdbcTemplate,
        clock: Clock,
        codes: SetupCodes,
        sessions: SetupSessions,
        codeAttempts: SetupCodeAttempts,
        adminSessions: SessionStore,
        signInAttempts: LoginAttemptTracker,
    ) = CleanInstallation(jdbc, clock as MovableClock, codes, sessions, codeAttempts, adminSessions, signInAttempts)
}

/** What the server answered. */
class Reply(
    val status: Int,
    private val headers: Map<String, List<String>>,
    val body: String,
    private val mapper: ObjectMapper,
) {
    val json: JsonNode by lazy { mapper.readTree(body.ifBlank { "null" }) }
    val code: String? get() = json.path("code").takeIf { !it.isMissingNode }?.asString()
    val contentType: String? get() = header("Content-Type")

    fun header(name: String): String? =
        headers.entries
            .firstOrNull { it.key.equals(name, true) }
            ?.value
            ?.firstOrNull()

    fun setCookies(): List<String> =
        headers.entries
            .firstOrNull { it.key.equals("Set-Cookie", true) }
            ?.value
            .orEmpty()

    /** The Set-Cookie line that sets [name], whole. */
    fun setCookie(name: String): String? = setCookies().firstOrNull { it.startsWith("$name=") }

    /** The value [name] is set to, or null when the response does not set it. */
    fun cookie(name: String): String? = setCookie(name)?.substringAfter("$name=")?.substringBefore(';')

    /** Status and body only: the response compared with another one. */
    fun sameAs(other: Reply) = status == other.status && contentType == other.contentType && body == other.body

    /** All the text of the response, headers and body: where a secret must not be. */
    fun everything(): String {
        val lines = headers.entries.flatMap { (name, values) -> values.map { "$name: $it" } }
        return lines.joinToString("\n") + "\n" + body
    }

    override fun toString() = "$status $body"
}

/** A client of a running server that sends the cookies it is told to, as the tests of the first start need. */
class FirstStartClient(
    private val port: Int,
    private val mapper: ObjectMapper,
) {
    private val http = HttpClient.newHttpClient()

    fun send(
        method: String,
        path: String,
        body: String? = null,
        cookies: Map<String, String> = emptyMap(),
        headers: Map<String, String> = emptyMap(),
        json: Boolean = true,
    ): Reply {
        val publisher = body?.let { HttpRequest.BodyPublishers.ofString(it) } ?: HttpRequest.BodyPublishers.noBody()
        val builder = HttpRequest.newBuilder(URI.create("http://localhost:$port$path")).method(method, publisher)
        if (json) builder.header("Content-Type", "application/json")
        if (cookies.isNotEmpty()) {
            builder.header("Cookie", cookies.map { (name, value) -> "$name=$value" }.joinToString("; "))
        }
        headers.forEach { (name, value) -> builder.header(name, value) }
        val response = http.send(builder.build(), HttpResponse.BodyHandlers.ofString())
        return Reply(response.statusCode(), response.headers().map(), response.body(), mapper)
    }

    private fun quoted(text: String) = mapper.writeValueAsString(text)

    /** A JSON text as a tree, to compare with what the server answered. */
    fun jsonOf(text: String): JsonNode = mapper.readTree(text)

    fun state(
        setup: String? = null,
        admin: String? = null,
    ) = send("GET", "/api/v1/onboarding", cookies = cookiesOf(setup, admin))

    fun enterCode(
        code: String,
        setup: String? = null,
        headers: Map<String, String> = emptyMap(),
    ) = enterRaw("""{"code":${quoted(code)}}""", setup, headers)

    fun enterRaw(
        body: String?,
        setup: String? = null,
        headers: Map<String, String> = emptyMap(),
        json: Boolean = true,
    ) = send("POST", "/api/v1/onboarding/setup-session", body, cookiesOf(setup, null), headers, json)

    fun confirmCa(
        setup: String?,
        headers: Map<String, String> = emptyMap(),
    ) = send("POST", "/api/v1/onboarding/ca", null, cookiesOf(setup, null), headers)

    fun admin(
        setup: String?,
        password: String,
        headers: Map<String, String> = emptyMap(),
    ) = adminRaw("""{"password":${quoted(password)}}""", setup, headers)

    fun adminRaw(
        body: String?,
        setup: String?,
        headers: Map<String, String> = emptyMap(),
        json: Boolean = true,
    ) = send("POST", "/api/v1/onboarding/admin", body, cookiesOf(setup, null), headers, json)

    fun login(
        password: String,
        headers: Map<String, String> = emptyMap(),
    ) = send("POST", "/api/v1/session", """{"password":${quoted(password)}}""", headers = headers)

    fun session(admin: String?) = send("GET", "/api/v1/session", cookies = cookiesOf(null, admin))

    /** The session is alive: GET /api/v1/session answers 200. */
    fun alive(admin: String) = session(admin).status == 200

    fun changePassword(
        admin: String?,
        current: String,
        new: String,
        headers: Map<String, String> = emptyMap(),
    ) = changeRaw("""{"currentPassword":${quoted(current)},"newPassword":${quoted(new)}}""", admin, headers)

    fun changeRaw(
        body: String?,
        admin: String?,
        headers: Map<String, String> = emptyMap(),
        json: Boolean = true,
    ) = send("PUT", "/api/v1/session/password", body, cookiesOf(null, admin), headers, json)

    private fun cookiesOf(
        setup: String?,
        admin: String?,
    ): Map<String, String> {
        val cookies = listOfNotNull(setup?.let { SETUP_COOKIE to it }, admin?.let { SESSION_COOKIE to it })
        return cookies.toMap()
    }

    // ---- The scenarios' own steps ----

    /** Enters the code and returns the setup session. */
    fun setupSession(code: String = CODE): String {
        val reply = enterCode(code)
        check(reply.status == 204) { "the code was refused: $reply" }
        return checkNotNull(reply.cookie(SETUP_COOKIE))
    }

    /** "Владелец прошёл мастер с паролем P": code, CA, password; returns the administrator session. */
    fun completeWizard(password: String = OWNER_PASSWORD): String {
        val setup = setupSession()
        val confirmed = confirmCa(setup)
        check(confirmed.status == 204) { "the CA was not confirmed: $confirmed" }
        val reply = admin(setup, password)
        check(reply.status == 204) { "the wizard refused the password: $reply" }
        return checkNotNull(reply.cookie(SESSION_COOKIE))
    }
}

/** The state of the server under test returned to a clean installation: no administrator, no step, a new code. */
class CleanInstallation(
    private val jdbc: JdbcTemplate,
    private val clock: MovableClock,
    private val codes: SetupCodes,
    private val sessions: SetupSessions,
    private val codeAttempts: SetupCodeAttempts,
    private val adminSessions: SessionStore,
    private val signInAttempts: LoginAttemptTracker,
) {
    fun restore() {
        jdbc.update("delete from administrators")
        jdbc.update("delete from onboarding_steps")
        clock.now = T0
        sessions.endAll()
        adminSessions.removeAll()
        codeAttempts.tracker.forgetAll()
        signInAttempts.forgetAll()
        codes.issue()
    }
}

/** Test-only: forget every address, as a restart does. */
fun LoginAttemptTracker.forgetAll() {
    val field = LoginAttemptTracker::class.java.getDeclaredField("byAddress")
    field.isAccessible = true
    (field.get(this) as MutableMap<*, *>).clear()
}
