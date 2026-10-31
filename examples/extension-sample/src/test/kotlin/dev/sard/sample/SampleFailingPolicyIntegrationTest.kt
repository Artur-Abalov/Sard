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

/** A policy that throws closes access; a listener that throws changes nothing (@sample). */
@SpringBootTest(
    classes = [SardServerApplication::class],
    webEnvironment = SpringBootTest.WebEnvironment.RANDOM_PORT,
    properties = [
        "spring.grpc.server.port=0",
        "sard.sample.policy-failing=true",
        "sard.sample.listener=throw",
        "sard.session.sweep-interval=1h",
    ],
)
@Import(SampleInstallation::class)
@ContextConfiguration(initializers = [FreshPkiDirectory::class])
class SampleFailingPolicyIntegrationTest(
    @Autowired mapper: ObjectMapper,
    @LocalServerPort port: Int,
) {
    private val client = SampleClient(port, mapper)

    @Test
    fun `Сбой политики закрывает доступ`() {
        val reply = client.send("GET", "/api/v1/sample/events", checkNotNull(client.signIn()))

        assertEquals(403, reply.status)
        assertEquals("access_denied", reply.json.path("code").asString())
    }

    @Test
    fun `Исключение слушателя не мешает входу`() {
        assertEquals(204, client.send("POST", "/api/v1/session", body = """{"password":"$ADMIN_PASSWORD"}""").status)
    }
}
