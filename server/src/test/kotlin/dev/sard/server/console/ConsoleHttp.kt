// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.console

import dev.sard.server.api.SESSION_COOKIE
import java.net.URI
import java.net.http.HttpClient
import java.net.http.HttpRequest
import java.net.http.HttpResponse

const val FIXTURE_CONSOLE = "classpath:/fixtures/console/"
const val FIXTURE_PAGE_MARK = "sard-console-fixture"
const val CONSOLE_PASSWORD = "correct-horse-battery"

/** A plain HTTP client of a running test server; paths are sent as written, never normalized. */
class ConsoleHttp(
    private val port: Int,
) {
    private val http = HttpClient.newHttpClient()

    fun send(
        method: String,
        path: String,
        cookie: String? = null,
        accept: String? = null,
        body: String? = null,
    ): HttpResponse<String> {
        val publisher = body?.let { HttpRequest.BodyPublishers.ofString(it) } ?: HttpRequest.BodyPublishers.noBody()
        val builder = HttpRequest.newBuilder(URI.create("http://localhost:$port$path")).method(method, publisher)
        if (body != null) builder.header("Content-Type", "application/json")
        cookie?.let { builder.header("Cookie", "$SESSION_COOKIE=$it") }
        accept?.let { builder.header("Accept", it) }
        return http.send(builder.build(), HttpResponse.BodyHandlers.ofString())
    }

    fun get(
        path: String,
        cookie: String? = null,
        accept: String? = null,
    ) = send("GET", path, cookie, accept)

    fun signIn(): String {
        val response = send("POST", "/api/v1/session", body = """{"password":"$CONSOLE_PASSWORD"}""")
        val setCookie = response.headers().firstValue("Set-Cookie").orElseThrow()
        return Regex("$SESSION_COOKIE=([^;]*)").find(setCookie)!!.groupValues[1]
    }
}

fun HttpResponse<String>.header(name: String): String? = headers().firstValue(name).orElse(null)

fun HttpResponse<String>.isConsolePage(expected: String): Boolean =
    statusCode() == 200 && header("Content-Type") == "text/html;charset=UTF-8" && body() == expected
