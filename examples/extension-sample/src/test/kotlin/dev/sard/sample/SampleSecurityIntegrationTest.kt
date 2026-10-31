// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.sample

import dev.sard.server.SardServerApplication
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.boot.test.web.server.LocalServerPort
import org.springframework.context.annotation.Import
import org.springframework.test.context.ContextConfiguration
import tools.jackson.databind.ObjectMapper
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNull

/** The security chain of the core with the sample (spring-security.feature, @sample, Р40, Р41, Р47). */
@SpringBootTest(
    classes = [SardServerApplication::class],
    webEnvironment = SpringBootTest.WebEnvironment.RANDOM_PORT,
    properties = [
        "spring.grpc.server.port=0",
        "sard.sample.public-ping=true",
        "sard.sample.deny=GET /api/v1/sources",
        "sard.session.sweep-interval=1h",
    ],
)
@Import(SampleInstallation::class)
@ContextConfiguration(initializers = [FreshPkiDirectory::class])
class SampleSecurityIntegrationTest(
    @Autowired mapper: ObjectMapper,
    @LocalServerPort port: Int,
) {
    private val client = SampleClient(port, mapper)

    private fun SampleClient.Reply.assertApiHeaders() {
        assertEquals(listOf("nosniff"), headers.entries.single { it.key.equals("X-Content-Type-Options", true) }.value)
        assertEquals(listOf("no-store"), headers.entries.single { it.key.equals("Cache-Control", true) }.value)
    }

    @Test
    fun `Публичная операция образца отвечает без сессии`() {
        val reply = client.send("GET", "/api/v1/sample/ping")

        assertEquals(204, reply.status)
        reply.assertApiHeaders()
    }

    @Test
    fun `Публичная операция образца не открывает соседние пути`() {
        assertEquals(401, client.send("GET", "/api/v1/sample/events").status)
        assertEquals(401, client.send("GET", "/api/v1/sample/ping/deeper").status)
    }

    @Test
    fun `Отказ политики несёт заголовки API`() {
        val reply = client.send("GET", "/api/v1/sources", checkNotNull(client.signIn()))

        assertEquals(403, reply.status)
        assertEquals("access_denied", reply.json.path("code").asString())
        reply.assertApiHeaders()
        assertNull(reply.headers.entries.firstOrNull { it.key.equals("Set-Cookie", true) })
    }
}
