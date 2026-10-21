// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server

import java.io.File
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

/** The variables of the metrics listener (H1 phase 3), as the operator sets them. */
internal val METRICS_VARIABLES =
    listOf("SARD_METRICS_BIND", "SARD_METRICS_PORT", "SARD_METRICS_TOKEN", "SARD_METRICS_STATE_INTERVAL")

/** What `deploy/docker-compose.yml` says about the metrics listener (H1 phase 3): reachable inside, never published. */
class DeployComposeTest {
    private val compose: List<String>
        get() {
            val file = File("../deploy/docker-compose.yml")
            check(file.isFile) { "expected to run with the server module as the working directory: $file" }
            return file.readLines()
        }

    /** The lines of the section [name] of the service `server` (its keys are indented six spaces). */
    private fun section(name: String): List<String> {
        val service = compose.dropWhile { it != "  server:" }.drop(1).takeWhile { !it.matches(Regex("""^  \S.*""")) }
        return service.dropWhile { it != "    $name:" }.drop(1).takeWhile { it.startsWith("      ") }
    }

    @Test
    fun `Docker-compose не публикует порт метрик`() {
        val ports = section("ports").filter { it.trim().startsWith("-") }

        assertEquals(2, ports.size, ports.toString())
        assertFalse(ports.any { "8081" in it || "SARD_METRICS" in it }, ports.toString())
    }

    @Test
    fun `Docker-compose передаёт серверу переменные метрик с умолчанием loopback и без токена`() {
        val environment = section("environment").filterNot { it.trim().startsWith("#") }.map { it.trim() }

        for (name in METRICS_VARIABLES) {
            assertTrue(environment.any { it.startsWith("$name:") }, name)
        }
        assertTrue(environment.contains("SARD_METRICS_BIND: \${SARD_METRICS_BIND:-127.0.0.1}"), environment.toString())
        assertTrue(environment.contains("SARD_METRICS_TOKEN: \${SARD_METRICS_TOKEN:-}"), environment.toString())
    }

    @Test
    fun `Образ не объявляет порт метрик через EXPOSE`() {
        val dockerfile = File("../deploy/server/Dockerfile").readLines()

        assertEquals(listOf("EXPOSE 8080 9090"), dockerfile.filter { it.startsWith("EXPOSE") })
    }
}
