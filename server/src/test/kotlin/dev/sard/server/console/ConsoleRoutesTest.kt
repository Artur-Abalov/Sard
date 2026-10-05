// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.console

import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import kotlin.test.Test
import kotlin.test.assertEquals

private val BUNDLE =
    setOf(
        "index.html",
        "favicon.svg",
        "assets/index-Ab12Cd34.js",
        "assets/index-Ab12Cd34.css",
        "assets/plex-Ef56Gh78.woff2",
    )

@MutFlowTest
class ConsoleRoutesTest {
    private val routes = ConsoleRoutes { it in BUNDLE }

    private fun route(
        path: String,
        method: String = "GET",
    ): ConsoleRoute = MutFlow.underTest { routes.route(method, path) }

    @Test
    fun `the root is the console page`() {
        assertEquals(ConsoleRoute.Page, route("/"))
    }

    @Test
    fun `api actuator and openapi paths are left to their owners by segment`() {
        listOf(
            "/api",
            "/api/",
            "/api/v1/status",
            "/api/v2/agents",
            "/actuator",
            "/actuator/health",
            "/actuator/env",
            "/v3",
            "/v3/api-docs",
        ).forEach { assertEquals(ConsoleRoute.PassThrough, route(it), it) }
    }

    @Test
    fun `a path that only starts like an excluded segment is a console path`() {
        assertEquals(ConsoleRoute.Page, route("/apiary"))
        assertEquals(ConsoleRoute.Page, route("/v3d"))
        assertEquals(ConsoleRoute.Page, route("/actuators/x"))
    }

    @Test
    fun `an excluded path answers every method as its owner does`() {
        assertEquals(ConsoleRoute.PassThrough, route("/api/v1/session", method = "POST"))
    }

    @Test
    fun `a method other than GET and HEAD on a console path is not allowed`() {
        listOf(
            "POST" to "/",
            "PUT" to "/agents",
            "DELETE" to "/favicon.svg",
            "PATCH" to "/index.html",
            "OPTIONS" to "/login",
            "POST" to "/assets/index-Ab12Cd34.js",
        ).forEach { (method, path) ->
            assertEquals(ConsoleRoute.MethodNotAllowed, route(path, method), "$method $path")
        }
    }

    @Test
    fun `GET and HEAD are served alike`() {
        assertEquals(ConsoleRoute.Page, route("/agents", "HEAD"))
        assertEquals(ConsoleRoute.File("favicon.svg"), route("/favicon.svg", "HEAD"))
    }

    @Test
    fun `a file of the bundle is served as it is`() {
        assertEquals(ConsoleRoute.File("favicon.svg"), route("/favicon.svg"))
        assertEquals(ConsoleRoute.File("assets/plex-Ef56Gh78.woff2"), route("/assets/plex-Ef56Gh78.woff2"))
    }

    @Test
    fun `index html by its file name is the console page`() {
        assertEquals(ConsoleRoute.Page, route("/index.html"))
    }

    @Test
    fun `a deep link without a dot in its last segment is the console page`() {
        listOf(
            "/login",
            "/agents/",
            "/agents/0192f7a0-0000-7000-8000-000000000101",
            "/sources/new",
            "/no-such-page/deeper",
            "/assets",
        ).forEach { assertEquals(ConsoleRoute.Page, route(it), it) }
    }

    @Test
    fun `a dot in an earlier segment does not make a deep link a file`() {
        assertEquals(ConsoleRoute.Page, route("/v1.2/agents"))
    }

    @Test
    fun `a missing file with a dot in its last segment is not found`() {
        listOf("/robots.txt", "/agents/report.pdf", "/mockServiceWorker.js", "/assets/missing.css", "/application.yaml")
            .forEach { assertEquals(ConsoleRoute.NotFound, route(it), it) }
    }

    @Test
    fun `a missing path under assets is not found even without a dot`() {
        assertEquals(ConsoleRoute.NotFound, route("/assets/no-extension"))
        assertEquals(ConsoleRoute.NotFound, route("/assets/deeper/still"))
    }

    @Test
    fun `a path that leaves the bundle directory is not found`() {
        listOf("/assets/../application.yaml", "/../application.yaml", "/assets/./x", "/agents/..", "/a\\b", "/a\u0000b")
            .forEach { assertEquals(ConsoleRoute.NotFound, route(it), it) }
    }
}
