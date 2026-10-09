// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.console

import dev.sard.server.SeededAdministrator
import dev.sard.server.TestcontainersConfiguration
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.boot.test.web.server.LocalServerPort
import org.springframework.context.annotation.Import
import org.springframework.core.io.ClassPathResource
import org.springframework.test.annotation.DirtiesContext
import org.testcontainers.postgresql.PostgreSQLContainer
import kotlin.test.Test
import kotlin.test.assertTrue

/** Scenario "Консоль отдаётся при недоступной базе данных"; the context is retired with its stopped container. */
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.RANDOM_PORT,
    properties = [
        "spring.grpc.server.port=0",
        "sard.test.admin-password=$CONSOLE_PASSWORD",
        "sard.console.location=$FIXTURE_CONSOLE",
    ],
)
@Import(SeededAdministrator::class, TestcontainersConfiguration::class)
@DirtiesContext
class ConsoleWithoutDatabaseIntegrationTest(
    @Autowired private val postgres: PostgreSQLContainer,
    @LocalServerPort port: Int,
) {
    private val http = ConsoleHttp(port)

    @Test
    fun `Консоль отдаётся при недоступной базе данных`() {
        postgres.stop()
        val page = ClassPathResource("fixtures/console/index.html").getContentAsString(Charsets.UTF_8)
        assertTrue(http.get("/agents").isConsolePage(page))
    }
}
