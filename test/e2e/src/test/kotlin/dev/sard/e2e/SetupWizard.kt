// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.e2e

import java.net.CookieManager
import java.net.URI
import java.net.http.HttpClient
import java.net.http.HttpRequest
import java.net.http.HttpResponse

/**
 * The setup code as the server prints it in its log (docs/specs/server/onboarding-setup.feature, Р1):
 * `SARD SETUP CODE: XXXX-XXXX-XXXX-XXXX-XXXX-XXXX-XXXX valid until <instant>`, in the Crockford
 * alphabet without I, L, O and U.
 */
internal object SetupCode {
    private val LINE = Regex("SARD SETUP CODE: ([0-9A-HJKMNP-TV-Z]{4}(?:-[0-9A-HJKMNP-TV-Z]{4}){6}) valid until (\\S+)")

    /** The code of the last start in [log], or null when there is no code line. */
    fun lastIn(log: String): String? =
        LINE
            .findAll(log)
            .lastOrNull()
            ?.groupValues
            ?.get(1)

    fun count(log: String): Int = LINE.findAll(log).count()
}

/**
 * The first start of an installation as the owner does it: the code from the server's log, the CA,
 * the administrator's password. The tests use it instead of a password in the environment, which the
 * server no longer reads.
 */
internal class SetupWizard(
    private val env: SardEnvironment,
) {
    private val http = HttpClient.newBuilder().cookieHandler(CookieManager()).build()

    /** True once the step admin is done: the administrator has a password. */
    fun administratorSet(): Boolean {
        val body = send("GET", "/api/v1/onboarding", null).second
        val admin = Regex("\\{[^{}]*\"id\"\\s*:\\s*\"admin\"[^{}]*}").find(body)?.value
        return admin?.contains("\"state\":\"done\"") == true || admin?.contains("\"state\" : \"done\"") == true
    }

    /** Goes through code, CA and administrator step with [password]; the server must be on a clean installation. */
    fun complete(password: String) {
        val code = Await.value("the setup code in the server log") { SetupCode.lastIn(env.serverLogs()) }
        env.secret(code)
        step("setup-session", """{"code":"$code"}""")
        step("ca", null)
        step("admin", """{"password":"$password"}""")
    }

    private fun step(
        name: String,
        body: String?,
    ) {
        val status = send("POST", "/api/v1/onboarding/$name", body).first
        check(status == NO_CONTENT) { "the step $name answered $status" }
    }

    private fun send(
        method: String,
        path: String,
        body: String?,
    ): Pair<Int, String> {
        val request =
            HttpRequest
                .newBuilder(URI.create(env.httpBase + path))
                .header("Content-Type", "application/json")
                .method(method, body?.let(HttpRequest.BodyPublishers::ofString) ?: HttpRequest.BodyPublishers.noBody())
                .build()
        val response = http.send(request, HttpResponse.BodyHandlers.ofString())
        return response.statusCode() to response.body()
    }

    private companion object {
        const val NO_CONTENT = 204
    }
}
