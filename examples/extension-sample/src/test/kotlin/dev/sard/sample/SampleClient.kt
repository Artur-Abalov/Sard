// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.sample

import tools.jackson.databind.JsonNode
import tools.jackson.databind.ObjectMapper
import java.net.URI
import java.net.http.HttpClient
import java.net.http.HttpRequest
import java.net.http.HttpResponse

/** A plain HTTP client of the server under test; the session is the cookie of the last sign-in. */
class SampleClient(
    private val port: Int,
    private val mapper: ObjectMapper,
) {
    private val http = HttpClient.newHttpClient()

    class Reply(
        val status: Int,
        val body: String,
        private val mapper: ObjectMapper,
    ) {
        val json: JsonNode by lazy { mapper.readTree(body) }

        override fun toString() = "$status $body"
    }

    fun signIn(password: String = ADMIN_PASSWORD): String? {
        val response = raw("POST", "/api/v1/session", """{"password":"$password"}""", emptyMap())
        val cookie = response.headers().firstValue("Set-Cookie").orElse(null) ?: return null
        return Regex("sard_session=([^;]+)").find(cookie)?.groupValues?.get(1)
    }

    fun send(
        method: String,
        path: String,
        session: String? = null,
        body: String? = null,
        headers: Map<String, String> = emptyMap(),
    ): Reply {
        val cookie = session?.let { mapOf("Cookie" to "sard_session=$it") }.orEmpty()
        val response = raw(method, path, body, headers + cookie)
        return Reply(response.statusCode(), response.body(), mapper)
    }

    private fun raw(
        method: String,
        path: String,
        body: String?,
        headers: Map<String, String>,
    ): HttpResponse<String> {
        val publisher = body?.let { HttpRequest.BodyPublishers.ofString(it) } ?: HttpRequest.BodyPublishers.noBody()
        val builder = HttpRequest.newBuilder(URI.create("http://localhost:$port$path")).method(method, publisher)
        if (body != null) builder.header("Content-Type", "application/json")
        headers.forEach { (name, value) -> builder.header(name, value) }
        return http.send(builder.build(), HttpResponse.BodyHandlers.ofString())
    }
}
