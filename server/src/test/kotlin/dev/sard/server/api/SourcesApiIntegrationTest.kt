// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import dev.sard.server.enrollment.Enrollment
import dev.sard.server.enrollment.EnrollmentTokens
import dev.sard.server.pki.CertificateAuthority
import dev.sard.server.pki.MovableClock
import dev.sard.server.registration.Registration
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.beans.factory.annotation.Value
import org.springframework.boot.test.web.server.LocalServerPort
import org.springframework.jdbc.core.JdbcTemplate
import tools.jackson.databind.ObjectMapper
import java.time.Duration
import java.util.UUID
import java.util.concurrent.Callable
import java.util.concurrent.CountDownLatch
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit
import kotlin.test.AfterTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

private const val SECRET_SCHEMA =
    """{"type":"object","properties":{"password":{"type":"string","format":"sard-secret"}},"required":["password"]}"""

/** Rules "Источник проверяется до того, как конфиг попадёт к агенту" and "Конфиг источника ... не попадают в логи". */
@RestApiTest
class SourcesApiIntegrationTest(
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
    private val agent = world.agent(tenant)

    @AfterTest
    fun `drop the tenants`() = world.close()

    private fun body(
        name: String = "etc",
        agentId: UUID = agent.agentId,
        plugin: String = "files",
        repository: String = "qa",
        config: String = """{"paths":["/etc"]}""",
    ) = """{"name":${mapper.writeValueAsString(
        name,
    )},"agentId":"$agentId","plugin":"$plugin","repositoryName":"$repository","config":$config}"""

    private fun create(
        body: String = body(),
        session: ApiSession = admin,
    ) = world.api.post("/api/v1/sources", session, body)

    private fun assertRefused(
        response: ApiResponse,
        code: String,
        field: String,
    ) {
        assertEquals(422, response.status, response.toString())
        assertEquals(code, response.code)
        assertTrue(field in response.errorFields(), "${response.errorFields()} lacks $field")
    }

    @Test
    fun `Создание источника отвечает 201 с источником`() {
        val response = create()

        assertEquals(201, response.status)
        val json = response.json
        assertTrue(json.path("id").asString().isNotEmpty())
        assertEquals(T0.toString(), json.path("createdAt").asString())
        assertEquals(T0.toString(), json.path("updatedAt").asString())
        assertEquals(
            listOf("etc", agent.agentId.toString(), "files", "qa"),
            listOf("name", "agentId", "plugin", "repositoryName").map {
                json.path(it).asString()
            },
        )
        assertEquals(mapper.readTree("""{"paths":["/etc"]}"""), json.path("config"))
        val id = json.path("id").asString()
        assertEquals(json, world.api.get("/api/v1/sources/$id", admin).json)
        assertEquals(
            listOf(id),
            world.api
                .get("/api/v1/sources", admin)
                .json
                .pluck("items", "id"),
        )
    }

    @Test
    fun `Источник с несуществующим агентом отклоняется`() {
        assertRefused(create(body(agentId = UUID.randomUUID())), "unknown_agent", "agentId")
    }

    @Test
    fun `Источник с плагином, которого нет у агента, отклоняется`() {
        assertRefused(create(body(plugin = "postgres")), "unknown_plugin", "plugin")
    }

    @Test
    fun `Источник агента, ни разу не присылавшего Register, отклоняется как неизвестный плагин`() {
        val silent = world.enroll(tenant)

        assertRefused(create(body(agentId = silent.agentId)), "unknown_plugin", "plugin")
    }

    @Test
    fun `Источник с репозиторием, которого нет у агента, отклоняется`() {
        assertRefused(create(body(repository = "offsite")), "unknown_repository", "repositoryName")
    }

    @Test
    fun `Неизвестный агент проверяется раньше плагина и репозитория`() {
        val response = create(body(agentId = UUID.randomUUID(), plugin = "postgres", repository = "offsite"))

        assertRefused(response, "unknown_agent", "agentId")
    }

    @Test
    fun `Неизвестный плагин проверяется раньше репозитория`() {
        assertRefused(create(body(plugin = "postgres", repository = "offsite")), "unknown_plugin", "plugin")
    }

    @Test
    fun `Конфиг, нарушающий схему плагина, отклоняется с путями всех неверных полей`() {
        val response = create(body(config = """{"paths":["etc"],"one_file_system":"yes"}"""))

        assertRefused(response, "invalid_config", "config/one_file_system")
        assertEquals(listOf("config/one_file_system", "config/paths/0"), response.errorFields())
        assertEquals(0, world.count("sources", tenant))
    }

    @Test
    fun `Конфиг без обязательного поля отклоняется с путём config`() {
        assertRefused(create(body(config = "{}")), "invalid_config", "config")
    }

    @Test
    fun `Конфиг с полем, которого нет в схеме, отклоняется`() {
        val response = create(body(config = """{"paths":["/etc"],"compression":"max"}"""))

        assertEquals(422, response.status)
        assertEquals("invalid_config", response.code)
        assertEquals(0, world.count("sources", tenant))
    }

    @Test
    fun `Конфиг проверяется по схеме из последнего Register агента`() {
        val strict = FILES_SCHEMA.replace("\"required\": [\"paths\"]", "\"required\": [\"paths\", \"exclude\"]")
        assertTrue(strict != FILES_SCHEMA)
        world.register(agent, snapshotOf(plugins = listOf(filesPlugin(schema = strict))))

        assertRefused(create(body(config = """{"paths":["/etc"]}""")), "invalid_config", "config")
    }

    @Test
    fun `Конфиг, ссылающийся на неизвестный секрет, отклоняется`() {
        world.register(
            agent,
            snapshotOf(
                plugins = listOf(filesPlugin(name = "pg", schema = SECRET_SCHEMA)),
                secretNames = listOf("db-password"),
            ),
        )

        val response = create(body(plugin = "pg", config = """{"password":"other-password"}"""))

        assertRefused(response, "invalid_config", "config/password")
        assertTrue(
            "other-password" in
                response.json
                    .path("errors")
                    .get(0)
                    .path("message")
                    .asString(),
        )
    }

    @Test
    fun `Конфиг с известным секретом принимается`() {
        world.register(
            agent,
            snapshotOf(
                plugins = listOf(filesPlugin(name = "pg", schema = SECRET_SCHEMA)),
                secretNames = listOf("db-password"),
            ),
        )

        val response = create(body(plugin = "pg", config = """{"password":"db-password"}"""))

        assertEquals(201, response.status)
        assertEquals(
            "db-password",
            world.api
                .get("/api/v1/sources/${response.json.path("id").asString()}", admin)
                .json
                .path("config")
                .path("password")
                .asString(),
        )
    }

    @Test
    fun `Проверка схемой не ловит то, что проверяет только агент`() {
        assertEquals(201, create(body(config = """{"paths":["/srv","/srv/www"]}""")).status)
    }

    @Test
    fun `Конфиг больше 64 КиБ отклоняется`() {
        val big = "я".repeat(32768) // 65536 bytes of UTF-8, plus the JSON around it
        val response = create(body(config = """{"paths":["/$big"]}"""))

        assertRefused(response, "validation_failed", "config")
    }

    @Test
    fun `Нескомпилируемая схема плагина отклоняет конфиг с ошибкой у поля config`() {
        world.register(agent, snapshotOf(plugins = listOf(filesPlugin(name = "broken", schema = """{"type": 5}"""))))

        assertRefused(create(body(plugin = "broken", config = "{}")), "invalid_config", "config")
    }

    @Test
    fun `Неверное имя источника отклоняется ошибкой у поля name`() {
        for (name in listOf("", "x".repeat(201))) {
            assertRefused(create(body(name = name)), "validation_failed", "name")
        }
    }

    @Test
    fun `Имя источника из 200 символов принимается`() {
        assertEquals(201, create(body(name = "x".repeat(200))).status)
    }

    @Test
    fun `Имя, занятое живым источником, отклоняется`() {
        assertEquals(201, create().status)

        assertRefused(create(), "validation_failed", "name")
    }

    @Test
    fun `Имя удалённого источника можно занять снова`() {
        val id = create().json.path("id").asString()
        assertEquals(204, world.api.send("DELETE", "/api/v1/sources/$id", admin).status)

        assertEquals(201, create().status)
    }

    @Test
    fun `Одинаковые имена источников в разных тенантах допустимы`() {
        assertEquals(201, create().status)
        val other = world.tenant()
        val otherAgent = world.agent(other)

        assertEquals(201, create(body(agentId = otherAgent.agentId), world.admin(other)).status)
    }

    @Test
    fun `Из двух одновременных созданий источника с одним именем успешно одно`() {
        val pool = Executors.newFixedThreadPool(2)
        val start = CountDownLatch(1)
        val results =
            (1..2).map {
                pool.submit(
                    Callable {
                        start.await()
                        create()
                    },
                )
            }
        start.countDown()
        val responses = results.map { it.get(RACE_SECONDS, TimeUnit.SECONDS) }
        pool.shutdown()

        assertEquals(listOf(201, 422), responses.map { it.status }.sorted())
        assertEquals(listOf("name"), responses.first { it.status == 422 }.errorFields())
        assertEquals(1, world.count("sources", tenant))
    }

    @Test
    fun `Неверное тело запроса источника отклоняется ошибкой у поля`() {
        val valid = body()
        val cases =
            listOf(
                valid.replace("\"name\":\"etc\",", "") to "name",
                body().replace(agent.agentId.toString(), "not-a-uuid") to "agentId",
                body(config = "[]") to "config",
                body(config = "null") to "config",
                "this is not JSON" to "",
            )
        for ((text, field) in cases) {
            assertRefused(create(text), "validation_failed", field)
        }
    }

    @Test
    fun `Список источников фильтруется по агенту`() {
        val other = world.agent(tenant)
        val s1 = create(body(name = "s1")).json.path("id").asString()
        val s2 = create(body(name = "s2")).json.path("id").asString()
        create(body(name = "s3", agentId = other.agentId))

        val ids =
            world.api
                .get("/api/v1/sources?agentId=${agent.agentId}", admin)
                .json
                .pluck("items", "id")

        assertEquals(setOf(s1, s2), ids.toSet())
    }

    // --- Изменение источника

    private fun replace(
        id: String,
        body: String,
        session: ApiSession = admin,
    ) = world.api.send("PUT", "/api/v1/sources/$id", session, body)

    @Test
    fun `Изменение источника заменяет его целиком`() {
        val created = create().json
        val id = created.path("id").asString()
        clock.now = T0.plus(Duration.ofMinutes(1))

        val response = replace(id, body(config = """{"paths":["/var/www"]}"""))

        assertEquals(200, response.status)
        assertEquals(mapper.readTree("""{"paths":["/var/www"]}"""), response.json.path("config"))
        assertEquals(created.path("createdAt"), response.json.path("createdAt"))
        assertEquals(T0.plus(Duration.ofMinutes(1)).toString(), response.json.path("updatedAt").asString())
    }

    @Test
    fun `Изменение источника проверяется так же, как создание`() {
        val id = create().json.path("id").asString()
        create(body(name = "other"))
        val before = world.api.get("/api/v1/sources/$id", admin).json
        val cases =
            listOf(
                body(agentId = UUID.randomUUID()) to "unknown_agent",
                body(plugin = "postgres") to "unknown_plugin",
                body(repository = "offsite") to "unknown_repository",
                body(config = """{"paths":["etc"]}""") to "invalid_config",
                body(name = "other") to "validation_failed",
            )
        for ((text, code) in cases) {
            val response = replace(id, text)
            assertEquals(422, response.status, response.toString())
            assertEquals(code, response.code)
        }
        assertEquals(before, world.api.get("/api/v1/sources/$id", admin).json)
    }

    @Test
    fun `Изменение удалённого источника отвечает 404`() {
        val id = create().json.path("id").asString()
        world.api.send("DELETE", "/api/v1/sources/$id", admin)

        val response = replace(id, body())

        assertEquals(404, response.status)
        assertEquals("not_found", response.code)
    }

    // --- Удаление источника

    @Test
    fun `Удаление источника отвечает 204 и убирает его из списка и карточки`() {
        val id = create().json.path("id").asString()

        val response = world.api.send("DELETE", "/api/v1/sources/$id", admin)

        assertEquals(204, response.status)
        assertEquals("", response.body)
        assertEquals(404, world.api.get("/api/v1/sources/$id", admin).status)
        assertEquals(
            emptyList(),
            world.api
                .get("/api/v1/sources", admin)
                .json
                .pluck("items", "id"),
        )
    }

    @Test
    fun `Повторное удаление источника отвечает 404`() {
        val id = create().json.path("id").asString()
        world.api.send("DELETE", "/api/v1/sources/$id", admin)

        val response = world.api.send("DELETE", "/api/v1/sources/$id", admin)

        assertEquals(404, response.status)
        assertEquals("not_found", response.code)
    }

    // --- Значения конфига не попадают в логи

    @Test
    fun `Значения конфига источника не попадают в логи сервера`() {
        val marker = "/srv/QA-MARKER-7f3a"
        val config = """{"paths":["$marker"]}"""
        val operations: List<() -> Unit> =
            listOf(
                { create(body(name = "a", config = config)) },
                {
                    val id = create(body(name = "b")).json.path("id").asString()
                    replace(id, body(name = "b", config = config))
                },
                {
                    val id = create(body(name = "c", config = config)).json.path("id").asString()
                    world.api.post("/api/v1/sources/$id/runs", admin, null)
                },
                { create(body(name = "d", config = """{"paths":["$marker"],"one_file_system":"yes"}""")) },
                {
                    val id = create(body(name = "e", config = config)).json.path("id").asString()
                    world.api.get("/api/v1/sources", admin)
                    world.api.get("/api/v1/sources/$id", admin)
                },
            )
        for (operation in operations) {
            val leaked = captureLogs(operation).filter { "QA-MARKER-7f3a" in it }
            assertTrue(leaked.isEmpty(), "the config reached the log: $leaked")
        }
    }

    private companion object {
        const val RACE_SECONDS = 20L
    }
}
