// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import dev.sard.server.agents.stream.TestAgent
import dev.sard.server.enrollment.Enrollment
import dev.sard.server.enrollment.EnrollmentTokens
import dev.sard.server.pki.CertificateAuthority
import dev.sard.server.pki.MovableClock
import dev.sard.server.registration.AgentSnapshot
import dev.sard.server.registration.RecoveryKey
import dev.sard.server.registration.Registration
import dev.sard.server.registration.RepositoryEntry
import dev.sard.server.role.AgentRole
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.beans.factory.annotation.Value
import org.springframework.boot.test.web.server.LocalServerPort
import org.springframework.jdbc.core.JdbcTemplate
import tools.jackson.databind.JsonNode
import tools.jackson.databind.ObjectMapper
import java.util.UUID
import kotlin.test.AfterTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

private val R1 = repositoryId('1')
private val R2 = repositoryId('2')

/** V3, rules "Список репозиториев показывает признак по repository_id" and "Карточка верификатора" (КС5-КС7). */
@RestApiTest
class RepositoriesApiIntegrationTest(
    @Autowired ca: CertificateAuthority,
    @Autowired enrollment: Enrollment,
    @Autowired tokens: EnrollmentTokens,
    @Autowired registration: Registration,
    @Autowired jdbc: JdbcTemplate,
    @Autowired clock: MovableClock,
    @Autowired private val mapper: ObjectMapper,
    @LocalServerPort port: Int,
    @Value("\${local.grpc.server.port}") grpc: Int,
) {
    private val world = RestWorld(ca, enrollment, tokens, registration, jdbc, clock, mapper, port, grpc)
    private val clock = world.clock
    private val tenant = world.tenant()
    private val admin = world.admin(tenant)

    @AfterTest
    fun `drop the tenants`() = world.close()

    private fun holds(
        repositoryId: String,
        name: String = "base",
        key: RecoveryKey = RecoveryKey.PRESENT,
    ) = snapshotOf(repositories = listOf(RepositoryEntry(name, "local", repositoryId, "", key)))

    private fun source(
        snapshot: AgentSnapshot,
        hostname: String = "src1",
        inTenant: UUID = tenant,
    ): TestAgent = world.agent(inTenant, snapshot, hostname)

    private fun verifier(snapshot: AgentSnapshot = snapshotOf(repositories = emptyList())) =
        world.agent(tenant, snapshot, "vrf1", role = AgentRole.VERIFIER)

    private fun repositories(session: ApiSession? = admin) = world.api.get("/api/v1/repositories", session)

    private fun item(
        id: String,
        response: JsonNode = repositories().json,
    ) = response.path("items").list().first { it.path("repositoryId").asString() == id }

    private fun setupOf(verifier: TestAgent): JsonNode =
        world.api
            .get("/api/v1/agents/${verifier.agentId}", admin)
            .json
            .path("verifierSetup")

    @Test
    fun `Репозиторий без ключа восстановления считается в withoutRecoveryKey`() {
        source(holds(R1, key = RecoveryKey.ABSENT))
        source(holds(R2, "extra", RecoveryKey.PRESENT), "src2")

        val json = repositories().json

        assertEquals(listOf(R1, R2), json.pluck("items", "repositoryId"))
        assertEquals(listOf("absent", "present"), json.pluck("items", "recoveryKey"))
        assertEquals(1, json.path("withoutRecoveryKey").asInt())
    }

    @Test
    fun `Признак репозитория берётся из самого позднего Register с известным значением`() {
        val a = source(holds(R1, "base", RecoveryKey.ABSENT))
        clock.now = T0.plusSeconds(60)
        val b = source(holds(R1, "base2", RecoveryKey.PRESENT), "src2")
        clock.now = T0.plusSeconds(120)
        val w = verifier(holds(R1, "base", RecoveryKey.UNKNOWN))

        val found = item(R1)

        assertEquals("present", found.path("recoveryKey").asString())
        assertEquals(b.agentId.toString(), found.path("reportedByAgentId").asString())
        assertEquals(T0.plusSeconds(60).toString(), found.path("reportedAt").asString())
        val holders = found.path("holders").list()
        val ids = holders.map { it.path("agentId").asString() }
        assertEquals(listOf(a, b, w).map { it.agentId.toString() }.sorted(), ids)
        val ofB = holders.first { it.path("agentId").asString() == b.agentId.toString() }
        assertEquals(setOf("agentId", "hostname", "role", "repositoryName"), ofB.propertyNames().toSet())
        assertEquals("base2", ofB.path("repositoryName").asString())
        assertEquals("src2", ofB.path("hostname").asString())
        assertEquals("source", ofB.path("role").asString())
        val ofW = holders.first { it.path("agentId").asString() == w.agentId.toString() }
        assertEquals("verifier", ofW.path("role").asString())
    }

    @Test
    fun `При равном времени Register признак берётся у меньшего agentId`() {
        val first = source(holds(R1, "base", RecoveryKey.ABSENT))
        val second = source(holds(R1, "base", RecoveryKey.PRESENT), "src2")
        val smaller = listOf(first, second).minBy { it.agentId.toString() }

        val found = item(R1)

        assertEquals(smaller.agentId.toString(), found.path("reportedByAgentId").asString())
        assertEquals(if (smaller == first) "absent" else "present", found.path("recoveryKey").asString())
    }

    @Test
    fun `Репозиторий только с неизвестным признаком не считается без ключа`() {
        source(holds(R1, key = RecoveryKey.UNKNOWN))

        val json = repositories().json

        assertEquals("unknown", item(R1, json).path("recoveryKey").asString())
        assertTrue(item(R1, json).path("reportedAt").isNull)
        assertTrue(item(R1, json).path("reportedByAgentId").isNull)
        assertEquals(0, json.path("withoutRecoveryKey").asInt())
    }

    @Test
    fun `Репозиторий без id не входит в список`() {
        source(snapshotOf(repositories = listOf(RepositoryEntry("base", "local", "", "", RecoveryKey.ABSENT))))

        val json = repositories().json

        assertTrue(json.path("items").isEmpty)
        assertEquals(0, json.path("withoutRecoveryKey").asInt())
    }

    @Test
    fun `Сообщение отозванного агента не учитывается`() {
        val agent = source(holds(R1, key = RecoveryKey.ABSENT))
        world.api.post("/api/v1/agents/${agent.agentId}/revoke", admin, null)

        val json = repositories().json

        assertTrue(json.path("items").isEmpty)
        assertEquals(0, json.path("withoutRecoveryKey").asInt())
    }

    @Test
    fun `Репозитории другого тенанта не видны`() {
        val other = world.tenant()
        source(holds(repositoryId('9'), "base", RecoveryKey.ABSENT), "src9", other)

        val json = repositories().json

        assertTrue(json.path("items").isEmpty)
        assertEquals(0, json.path("withoutRecoveryKey").asInt())
    }

    @Test
    fun `Список репозиториев требует сессии администратора`() {
        assertEquals(401, repositories(session = null).status)
    }

    @Test
    fun `Репозиторий без ключа восстановления получает команду для источника`() {
        source(holds(R1, key = RecoveryKey.ABSENT))

        assertEquals("sudo sard-agent repo key add base --for recovery", item(R1).path("recoveryKeyCommand").asString())
    }

    @Test
    fun `Репозиторий с ключом восстановления команды не получает`() {
        source(holds(R1, key = RecoveryKey.PRESENT))

        assertTrue(item(R1).path("recoveryKeyCommand").isNull)
    }

    @Test
    fun `Репозиторий только у верификатора команды не получает`() {
        verifier(holds(R1, key = RecoveryKey.ABSENT))

        val found = item(R1)

        assertEquals("absent", found.path("recoveryKey").asString())
        assertTrue(found.path("recoveryKeyCommand").isNull)
    }

    @Test
    fun `Репозиторий встроенного агента получает команду самобэкапа`() {
        world.agent(tenant, holds(R1, "self", RecoveryKey.ABSENT), "sard-self", builtin = true)

        assertTrue(
            item(R1).path("recoveryKeyCommand").asString().endsWith(
                "self-agent repo key add self --for recovery --if-missing --config /etc/sard/self/agent.yaml",
            ),
        )
    }

    @Test
    fun `Список репозиториев не содержит сведений о ключах, кроме признака`() {
        source(holds(R1, key = RecoveryKey.PRESENT))

        val body = repositories().body

        for (word in listOf("password", "keyId", "label", "keys")) assertFalse(word in body, word)
    }

    @Test
    fun `Карточка верификатора содержит команду key add для источника и шаблон repo add`() {
        val a = source(holds(R1, "base", RecoveryKey.PRESENT))
        val w = verifier()

        val setup = setupOf(w).list().single()

        val label =
            w.agentId
                .toString()
                .replace("-", "")
                .take(8)
        assertEquals(
            setOf(
                "repositoryId",
                "sourceAgentId",
                "sourceHostname",
                "repositoryName",
                "keyAddCommand",
                "repoAddTemplate",
            ),
            setup.propertyNames().toSet(),
        )
        assertEquals(R1, setup.path("repositoryId").asString())
        assertEquals(a.agentId.toString(), setup.path("sourceAgentId").asString())
        assertEquals("src1", setup.path("sourceHostname").asString())
        assertEquals("base", setup.path("repositoryName").asString())
        val keyAdd = "sudo sard-agent repo key add base --for verifier --label vrf-$label"
        assertEquals(keyAdd, setup.path("keyAddCommand").asString())
        val repoAdd = "sudo sard-agent repo add base <address> --password-from-file <file>"
        assertEquals(repoAdd, setup.path("repoAddTemplate").asString())
    }

    @Test
    fun `Репозиторий, который верификатор уже держит, не предлагается`() {
        source(holds(R1))
        val w = verifier(holds(R1))

        assertTrue(setupOf(w).isEmpty)
    }

    @Test
    fun `Источник для команды - держатель с самым поздним Register`() {
        source(holds(R1, "base"))
        clock.now = T0.plusSeconds(60)
        val b = source(holds(R1, "main"), "src2")
        val w = verifier()

        val setup = setupOf(w).list().single()

        assertEquals(b.agentId.toString(), setup.path("sourceAgentId").asString())
        assertTrue("repo key add main" in setup.path("keyAddCommand").asString())
    }

    @Test
    fun `У агента-источника нет блока верификатора`() {
        val a = source(holds(R1))

        assertTrue(
            world.api
                .get("/api/v1/agents/${a.agentId}", admin)
                .json
                .path("verifierSetup")
                .isNull,
        )
    }

    @Test
    fun `Отозванный источник не предлагается верификатору`() {
        val a = source(holds(R1))
        val w = verifier()
        world.api.post("/api/v1/agents/${a.agentId}/revoke", admin, null)

        assertTrue(setupOf(w).isEmpty)
    }
}
