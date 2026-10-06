// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.console

import dev.sard.server.TestcontainersConfiguration
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.boot.test.web.server.LocalServerPort
import org.springframework.context.annotation.Import
import tools.jackson.databind.ObjectMapper
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

/** Rule "Сервер без консоли отвечает 404 на пути консоли и работает как прежде" (@http). */
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.RANDOM_PORT,
    properties = [
        "spring.grpc.server.port=0",
        "SARD_ADMIN_PASSWORD=$CONSOLE_PASSWORD",
        "sard.console.location=classpath:/fixtures/",
    ],
)
@Import(TestcontainersConfiguration::class)
class ConsoleAbsentIntegrationTest(
    @Autowired private val mapper: ObjectMapper,
    @LocalServerPort port: Int,
) {
    private val http = ConsoleHttp(port)

    @Test
    fun `Путь консоли на сервере без консоли отвечает 404`() {
        listOf("/", "/agents", "/index.html", "/assets/index-Ab12Cd34.js").forEach {
            val response = http.get(it)
            assertEquals(404, response.statusCode(), it)
            assertFalse((response.header("Content-Type") ?: "").startsWith("text/html"), it)
        }
    }

    @Test
    fun `Статус сервера работает на сервере без консоли`() {
        val response = http.get("/api/v1/status")
        assertEquals(200, response.statusCode())
        assertTrue(mapper.readTree(response.body()).has("version"))
    }
}
