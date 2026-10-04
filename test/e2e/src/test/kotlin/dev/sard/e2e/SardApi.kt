// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.e2e

import java.net.CookieManager
import java.net.URI
import java.net.http.HttpClient
import java.net.http.HttpRequest
import java.net.http.HttpResponse

/**
 * The server's REST API as the administrator sees it: signed in once with `SARD_ADMIN_PASSWORD`,
 * the session cookie kept in the client. No Origin header is sent, as from sardctl or curl
 * (the origin check lets such requests through, ADR 0021).
 *
 * Answers are read with [field]: the tests need a few top-level values, not a JSON model.
 */
internal class SardApi(
    private val env: SardEnvironment,
) {
    private val http = HttpClient.newBuilder().cookieHandler(CookieManager()).build()

    init {
        val signIn = send("POST", "/api/v1/session", """{"password":"${env.adminPassword}"}""")
        check(signIn.status == HTTP_NO_CONTENT) { "sign-in answered ${signIn.status}" }
    }

    class Answer(
        val status: Int,
        val body: String,
    ) {
        /** The first string or number value named [name] in the body. */
        fun field(name: String): String? =
            Regex("\"${Regex.escape(name)}\"\\s*:\\s*(?:\"([^\"]*)\"|(-?[0-9]+))")
                .find(body)
                ?.let { it.groupValues[1].ifEmpty { it.groupValues[2] } }

        fun require(name: String): String = checkNotNull(field(name)) { "the answer has no $name: $status" }

        override fun toString() = "Answer(status=$status)"
    }

    fun get(path: String): Answer = send("GET", path, null)

    fun post(
        path: String,
        body: String? = null,
    ): Answer = send("POST", path, body)

    /** POSTs and checks the status is [expected]; the body is not put in the failure (it may hold a token). */
    fun created(
        path: String,
        body: String? = null,
        expected: Int = HTTP_CREATED,
    ): Answer = post(path, body).also { check(it.status == expected) { "POST $path answered ${it.status}" } }

    private fun send(
        method: String,
        path: String,
        body: String?,
    ): Answer {
        val request =
            HttpRequest
                .newBuilder(URI.create(env.httpBase + path))
                .header("Content-Type", "application/json")
                .method(method, body?.let(HttpRequest.BodyPublishers::ofString) ?: HttpRequest.BodyPublishers.noBody())
                .build()
        val response = http.send(request, HttpResponse.BodyHandlers.ofString())
        return Answer(response.statusCode(), response.body())
    }

    companion object {
        const val HTTP_CREATED = 201
        const val HTTP_NO_CONTENT = 204
        const val HTTP_OK = 200
    }
}
