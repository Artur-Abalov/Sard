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

/** The policy and the failing licence of the sample (server-core-extensions.feature, @sample). */
@SpringBootTest(
    classes = [SardServerApplication::class],
    webEnvironment = SpringBootTest.WebEnvironment.RANDOM_PORT,
    properties = [
        "spring.grpc.server.port=0",
        "sard.sample.licence=failing",
        "sard.sample.deny=POST /api/v1/sample/notes, GET /api/v1/server",
        "sard.session.sweep-interval=1h",
    ],
)
@Import(SampleInstallation::class)
@ContextConfiguration(initializers = [FreshPkiDirectory::class])
class SamplePolicyIntegrationTest(
    @Autowired mapper: ObjectMapper,
    @LocalServerPort port: Int,
) {
    private val client = SampleClient(port, mapper)

    @Test
    fun `Эндпоинт расширения подчинён политике`() {
        val reply = client.send("POST", "/api/v1/sample/notes", checkNotNull(client.signIn()))

        assertEquals(403, reply.status)
        assertEquals("access_denied", reply.json.path("code").asString())
    }

    @Test
    fun `Запрет одной операции не мешает другим`() {
        assertEquals(200, client.send("GET", "/api/v1/sample/events", checkNotNull(client.signIn())).status)
    }

    @Test
    fun `Сбой шва лицензии не ломает статус`() {
        val reply = client.send("GET", "/api/v1/status")

        assertEquals(200, reply.status)
        assertEquals("sample", reply.json.path("edition").path("id").asString())
    }

    @Test
    fun `Публичные операции и выход политике не подчинены`() {
        val session = checkNotNull(client.signIn())

        assertEquals(204, client.send("DELETE", "/api/v1/session", session).status)
    }
}
