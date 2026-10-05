// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.e2e

import org.junit.jupiter.api.extension.RegisterExtension
import java.net.URI
import java.net.http.HttpClient
import java.net.http.HttpRequest
import java.net.http.HttpResponse
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

/** The @e2e scenarios of docs/specs/server/console-serving.feature: the image carries the console of its own version. */
class ConsoleImageTest {
    private fun get(path: String): HttpResponse<String> =
        HttpClient.newHttpClient().send(
            HttpRequest.newBuilder(URI.create("${sard.httpBase}$path")).build(),
            HttpResponse.BodyHandlers.ofString(),
        )

    @Test
    fun `Образ сервера отдаёт страницу консоли той же версии, что и сервер`() {
        val page = get("/")
        assertEquals(200, page.statusCode())
        assertTrue(page.headers().firstValue("Content-Type").orElse("").startsWith("text/html"), page.headers().toString())
        assertEquals(E2e.version, CONSOLE_VERSION.find(page.body())?.groupValues?.get(1), page.body())
        assertEquals(E2e.version, SERVER_VERSION.find(get("/api/v1/status").body())?.groupValues?.get(1))
    }

    @Test
    fun `Скрипт, на который ссылается страница консоли в образе, отдаётся`() {
        val assets = ASSET_REFERENCE.findAll(get("/").body()).map { it.groupValues[1] }.toList()
        assertTrue(assets.any { it.endsWith(".js") }, "the page references no script: $assets")
        assets.forEach {
            val response = get(it)
            assertEquals(200, response.statusCode(), it)
            assertEquals("public, max-age=31536000, immutable", response.headers().firstValue("Cache-Control").orElse(null), it)
        }
    }

    @Test
    fun `Образ сервера не содержит воркер моков`() {
        assertEquals(404, get("/mockServiceWorker.js").statusCode())
    }

    companion object {
        @JvmField
        @RegisterExtension
        val sard = SardEnvironment()

        private val CONSOLE_VERSION = Regex("""<meta name="sard-version" content="([^"]*)"""")
        private val SERVER_VERSION = Regex(""""version"\s*:\s*"([^"]*)"""")
        private val ASSET_REFERENCE = Regex("""(?:src|href)="(/assets/[^"]+)"""")
    }
}
