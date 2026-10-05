// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.console

import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import org.springframework.core.io.ClassPathResource
import org.springframework.core.io.FileUrlResource
import org.springframework.mock.web.MockHttpServletRequest
import org.springframework.mock.web.MockHttpServletResponse
import kotlin.test.Test
import kotlin.test.assertContentEquals
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNull

private const val FIXTURES = "fixtures/console/"
private const val CSP =
    "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; " +
        "font-src 'self' data:; connect-src 'self'; object-src 'none'; base-uri 'none'; " +
        "form-action 'self'; frame-ancestors 'none'"
private const val DEEP_LINK = "/agents/0192f7a0-0000-7000-8000-000000000101"
private const val IMMUTABLE = "public, max-age=31536000, immutable"

private class Served(
    val response: MockHttpServletResponse,
    val reachedChain: Boolean,
)

@MutFlowTest
class ConsoleFilterTest {
    private val filter = ConsoleFilter(ConsoleBundle(ClassPathResource(FIXTURES)))

    private fun fixture(path: String): ByteArray = ClassPathResource(FIXTURES + path).inputStream.use { it.readBytes() }

    private fun serve(
        uri: String,
        method: String = "GET",
        filter: ConsoleFilter = this.filter,
    ): Served {
        val response = MockHttpServletResponse()
        var reached = false
        MutFlow.underTest { filter.doFilter(MockHttpServletRequest(method, uri), response) { _, _ -> reached = true } }
        return Served(response, reached)
    }

    @Test
    fun `the root answers the console page byte for byte`() {
        val served = serve("/")
        assertEquals(200, served.response.status)
        assertEquals("text/html;charset=UTF-8", served.response.getHeader("Content-Type"))
        assertContentEquals(fixture("index.html"), served.response.contentAsByteArray)
        assertFalse(served.reachedChain)
    }

    @Test
    fun `a file of the bundle is answered as it is with its own content type`() {
        mapOf(
            "/assets/index-Ab12Cd34.js" to "text/javascript",
            "/assets/index-Ab12Cd34.css" to "text/css",
            "/assets/plex-Ef56Gh78.woff2" to "font/woff2",
            "/favicon.svg" to "image/svg+xml",
        ).forEach { (path, type) ->
            val served = serve(path)
            assertEquals(200, served.response.status, path)
            assertEquals(type, served.response.getHeader("Content-Type"), path)
            assertContentEquals(fixture(path.removePrefix("/")), served.response.contentAsByteArray, path)
            val length = fixture(path.removePrefix("/")).size.toLong()
            assertEquals(length, served.response.getHeader("Content-Length")?.toLong(), path)
        }
    }

    @Test
    fun `hashed files under assets are cached for good and the page and root files are not`() {
        assertEquals(IMMUTABLE, serve("/assets/index-Ab12Cd34.js").response.getHeader("Cache-Control"))
        assertEquals(IMMUTABLE, serve("/assets/plex-Ef56Gh78.woff2").response.getHeader("Cache-Control"))
        listOf("/", "/index.html", "/agents/0192f7a0-0000-7000-8000-000000000101", "/favicon.svg").forEach {
            assertEquals("no-cache", serve(it).response.getHeader("Cache-Control"), it)
        }
    }

    @Test
    fun `HEAD answers the headers of the page without a body`() {
        val served = serve("/agents/0192f7a0-0000-7000-8000-000000000101", "HEAD")
        assertEquals(200, served.response.status)
        assertEquals("text/html;charset=UTF-8", served.response.getHeader("Content-Type"))
        assertEquals("no-cache", served.response.getHeader("Cache-Control"))
        assertEquals(fixture("index.html").size.toLong(), served.response.getHeader("Content-Length")?.toLong())
        assertEquals(0, served.response.contentAsByteArray.size)
    }

    @Test
    fun `every page and file answer carries the security headers`() {
        listOf("/", DEEP_LINK, "/assets/index-Ab12Cd34.js", "/favicon.svg").forEach {
            val response = serve(it).response
            assertEquals("nosniff", response.getHeader("X-Content-Type-Options"), it)
            assertEquals("DENY", response.getHeader("X-Frame-Options"), it)
            assertEquals("same-origin", response.getHeader("Referrer-Policy"), it)
            assertEquals(CSP, response.getHeader("Content-Security-Policy"), it)
            assertNull(response.getHeader("Strict-Transport-Security"), it)
            assertNull(response.getHeader("Set-Cookie"), it)
        }
    }

    @Test
    fun `a missing asset is a bodiless 404 that is not cached for good`() {
        val served = serve("/assets/index-Zz00Zz00.js")
        assertEquals(404, served.response.status)
        assertEquals(0, served.response.contentAsByteArray.size)
        assertNull(served.response.getHeader("Cache-Control"))
        assertNull(served.response.getHeader("Content-Type"))
        assertFalse(served.reachedChain)
    }

    @Test
    fun `a missing file on HEAD is 404`() {
        assertEquals(404, serve("/robots.txt", "HEAD").response.status)
    }

    @Test
    fun `a method other than GET and HEAD is 405 and lists the two allowed`() {
        val served = serve("/agents", "POST")
        assertEquals(405, served.response.status)
        assertEquals("GET, HEAD", served.response.getHeader("Allow"))
        assertEquals(0, served.response.contentAsByteArray.size)
        assertFalse(served.reachedChain)
    }

    @Test
    fun `api actuator and openapi paths go on to their owners untouched`() {
        listOf("/api/v1/status", "/api", "/actuator/health", "/v3/api-docs").forEach {
            val served = serve(it)
            assertEquals(true, served.reachedChain, it)
            assertNull(served.response.getHeader("Content-Security-Policy"), it)
            assertNull(served.response.getHeader("Cache-Control"), it)
        }
    }

    @Test
    fun `an encoded dot segment cannot reach a file outside the bundle`() {
        listOf(
            "/assets/%2e%2e/application.yaml",
            "/%2e%2e/application.yaml",
            "/assets/%2e%2e%2f%2e%2e%2fapplication.yaml",
        ).forEach {
            val served = serve(it)
            assertEquals(404, served.response.status, it)
            assertFalse(served.response.contentAsString.contains("datasource"), it)
        }
    }

    @Test
    fun `an encoded path is decoded before it is looked up`() {
        assertContentEquals(fixture("favicon.svg"), serve("/favicon%2Esvg").response.contentAsByteArray)
    }

    @Test
    fun `a malformed escape is a 404`() {
        assertEquals(404, serve("/agents/%zz").response.status)
    }

    @Test
    fun `a double encoded dot segment does not leave a file location of the bundle`() {
        val onDisk = ConsoleFilter(ConsoleBundle(FileUrlResource(ClassPathResource(FIXTURES).url)))
        listOf("/assets/%252e%252e/%252e%252e/application.yaml", "/%252e%252e/application.yaml").forEach {
            val served = serve(it, filter = onDisk)
            assertEquals(404, served.response.status, it)
            assertFalse(served.response.contentAsString.contains("fixture-secret"), it)
        }
    }
}
