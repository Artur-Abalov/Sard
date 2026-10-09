// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.selfagent

import dev.sard.server.TestcontainersConfiguration
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.context.ApplicationContext
import org.springframework.context.annotation.Import
import org.springframework.jdbc.core.JdbcTemplate
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNull

/** Rule "Без канала механизм встроенного агента выключен" with the whole server and its database. */
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.RANDOM_PORT,
    properties = ["spring.grpc.server.port=0", "sard.self-agent.dir="],
)
@Import(TestcontainersConfiguration::class)
class SelfAgentOffIntegrationTest(
    @Autowired private val context: ApplicationContext,
    @Autowired private val jdbc: JdbcTemplate,
) {
    @Test
    fun `Сервер без канала не задаёт пароль роли sard_self и не выпускает встроенных токенов`() {
        assertEquals(0, context.getBeansOfType(SelfAgentLoop::class.java).size)
        val sql = "select rolpassword from pg_authid where rolname = 'sard_self'"
        assertNull(jdbc.queryForObject(sql, String::class.java))
        assertEquals(0, jdbc.queryForObject("select count(*) from enrollment_tokens where builtin", Int::class.java))
    }
}
