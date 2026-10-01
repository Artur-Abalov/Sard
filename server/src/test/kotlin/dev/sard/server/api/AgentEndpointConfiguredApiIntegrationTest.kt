// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import dev.sard.server.TestcontainersConfiguration
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.boot.test.web.server.LocalServerPort
import org.springframework.context.annotation.Import
import tools.jackson.databind.ObjectMapper
import kotlin.test.Test
import kotlin.test.assertEquals

/** Rule "Токены регистрации": SARD_AGENT_ENDPOINT "задан как localhost:9090" makes the answer say so. */
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.RANDOM_PORT,
    properties = ["spring.grpc.server.port=0", "sard.agent.endpoint=localhost:9090"],
)
@Import(TestcontainersConfiguration::class, RestApiTestConfiguration::class)
class AgentEndpointConfiguredApiIntegrationTest(
    @Autowired mapper: ObjectMapper,
    @LocalServerPort port: Int,
) {
    private val api = ApiClient(port, mapper)

    @Test
    fun `Ответ на создание говорит, задан ли адрес для агентов явно - задан`() {
        val response = api.post("/api/v1/enrollment-tokens", api.signIn())

        assertEquals(201, response.status)
        assertEquals(true, response.json.path("agentEndpointConfigured").asBoolean())
        val token = response.json.path("token").asString()
        assertEquals("sard-agent enroll --server localhost:9090 --token $token", response.json.path("enrollCommand").asString())
    }
}
