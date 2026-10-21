// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import dev.sard.server.enrollment.Enrollment
import dev.sard.server.enrollment.EnrollmentTokens
import dev.sard.server.pki.CertificateAuthority
import dev.sard.server.pki.MovableClock
import dev.sard.server.registration.AgentSnapshot
import dev.sard.server.registration.RecoveryKey
import dev.sard.server.registration.Registration
import dev.sard.server.registration.RepositoryEntry
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.beans.factory.annotation.Value
import org.springframework.boot.test.web.server.LocalServerPort
import org.springframework.jdbc.core.JdbcTemplate
import tools.jackson.databind.ObjectMapper
import java.util.UUID
import kotlin.test.AfterTest
import kotlin.test.Test
import kotlin.test.assertEquals

/** A repository id as restic prints it: 64 lower-case hex characters, all [digit]. */
fun repositoryId(digit: Char): String = digit.toString().repeat(64)

/** V3, rule "Сервер хранит признак ключа восстановления из Register агента" (КС4). */
@RestApiTest
class RecoveryKeyAgentsApiIntegrationTest(
    @Autowired ca: CertificateAuthority,
    @Autowired enrollment: Enrollment,
    @Autowired tokens: EnrollmentTokens,
    @Autowired registration: Registration,
    @Autowired private val jdbc: JdbcTemplate,
    @Autowired clock: MovableClock,
    @Autowired mapper: ObjectMapper,
    @LocalServerPort port: Int,
    @Value("\${local.grpc.server.port}") grpc: Int,
) {
    private val world = RestWorld(ca, enrollment, tokens, registration, jdbc, clock, mapper, port, grpc)
    private val tenant = world.tenant()
    private val admin = world.admin(tenant)

    @AfterTest
    fun `drop the tenants`() = world.close()

    private fun base(key: RecoveryKey): AgentSnapshot {
        val repository = RepositoryEntry("base", "local", repositoryId('1'), "", key)
        return snapshotOf(repositories = listOf(repository))
    }

    private fun flagOnCard(agent: UUID) =
        world.api
            .get("/api/v1/agents/$agent", admin)
            .json
            .path("repositories")
            .get(0)
            .path("recoveryKey")
            .asString()

    @Test
    fun `Карточка агента показывает признак ключа восстановления репозитория`() {
        val shown = RecoveryKey.entries.associateWith { it.stored }
        for ((key, text) in shown) {
            val agent = world.agent(tenant, base(key))

            assertEquals(text, flagOnCard(agent.agentId), key.name)
        }
    }

    @Test
    fun `Список агентов показывает признак ключа восстановления репозитория`() {
        val agent = world.agent(tenant, base(RecoveryKey.ABSENT))

        val item =
            world.api
                .get("/api/v1/agents?limit=200", admin)
                .json
                .path("items")
                .list()
                .first { it.path("id").asString() == agent.agentId.toString() }

        assertEquals(listOf("base"), item.pluck("repositories", "name"))
        assertEquals(listOf("absent"), item.pluck("repositories", "recoveryKey"))
    }

    @Test
    fun `Новый Register заменяет признак агента`() {
        val agent = world.agent(tenant, base(RecoveryKey.ABSENT))
        world.register(agent, base(RecoveryKey.PRESENT))

        assertEquals("present", flagOnCard(agent.agentId))
    }

    @Test
    fun `Признак хранится в базе и переживает перезапуск сервера`() {
        val agent = world.agent(tenant, base(RecoveryKey.ABSENT))

        val stored =
            jdbc.queryForObject(
                "select recovery_key from agent_repositories where agent_id = ?",
                String::class.java,
                agent.agentId,
            )

        assertEquals("absent", stored)
    }
}
