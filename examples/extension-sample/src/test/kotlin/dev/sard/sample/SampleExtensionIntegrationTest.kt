// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.sample

import dev.sard.server.SardServerApplication
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.boot.test.web.server.LocalServerPort
import org.springframework.context.annotation.Import
import org.springframework.test.context.ContextConfiguration
import org.springframework.jdbc.core.JdbcTemplate
import org.testcontainers.postgresql.PostgreSQLContainer
import tools.jackson.databind.ObjectMapper
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

/** The sample on a server of its own: server-core from ~/.m2 plus the starter (server-core-extensions.feature, @sample). */
@SpringBootTest(
    classes = [SardServerApplication::class],
    webEnvironment = SpringBootTest.WebEnvironment.RANDOM_PORT,
    properties = [
        "spring.grpc.server.port=0",
        "sard.sample.licence=expired",
        "sard.sample.listener=record",
        "sard.session.sweep-interval=1h",
    ],
)
@Import(SampleInstallation::class)
@ContextConfiguration(initializers = [FreshPkiDirectory::class])
class SampleExtensionIntegrationTest(
    @Autowired mapper: ObjectMapper,
    @Autowired private val postgres: PostgreSQLContainer,
    @Autowired private val jdbc: JdbcTemplate,
    @LocalServerPort port: Int,
) {
    private val client = SampleClient(port, mapper)

    private fun admin() = checkNotNull(client.signIn())

    private fun history(table: String) =
        SampleDatabase.query(postgres, "select count(*) from sard.$table where version = '1'") { it.getInt(1) }

    @Test
    fun `Миграция образца применяется после ядра в истории расширений`() {
        assertEquals(1, history("flyway_schema_history_extensions"))
        val inCore =
            SampleDatabase.query(postgres, "select count(*) from sard.flyway_schema_history where script like '%sample%'") {
                it.getInt(1)
            }
        assertEquals(0, inCore)
    }

    @Test
    fun `Статус сервера с образцом сообщает его редакцию`() {
        val json = client.send("GET", "/api/v1/status").json

        assertEquals("""{"id":"sample","displayName":"Sample"}""", json.path("edition").toString())
        assertTrue(!json.has("extensions") && !json.has("licence"))
    }

    @Test
    fun `Сведения о сервере с образцом сообщают его редакцию и его самого`() {
        val json = client.send("GET", "/api/v1/server", admin()).json

        assertEquals("""[{"id":"sample","displayName":"Sard Sample Extension"}]""", json.path("extensions").toString())
        assertEquals("expired", json.path("licence").path("state").asString())
        assertEquals("2026-10-01T00:00:00Z", json.path("licence").path("expiresAt").asString())
    }

    @Test
    fun `Эндпоинт расширения без сессии отвечает 401, с сессией 200`() {
        assertEquals(401, client.send("GET", "/api/v1/sample/events").status)
        assertEquals(401, client.send("GET", "/api/v1/sample/events", "forged-session-id").status)
        assertEquals(200, client.send("GET", "/api/v1/sample/events", admin()).status)
    }

    @Test
    fun `Изменяющий эндпоинт расширения с чужим Origin отвечает 403`() {
        val reply = client.send("POST", "/api/v1/sample/notes", admin(), headers = mapOf("Origin" to "https://evil.example"))

        assertEquals(403, reply.status)
        assertEquals("origin_rejected", reply.json.path("code").asString())
        assertEquals(204, client.send("POST", "/api/v1/sample/notes", admin()).status)
    }

    @Test
    fun `Эндпоинты расширения есть в OpenAPI сервера с требованием сессии`() {
        val spec = client.send("GET", "/v3/api-docs").json

        for ((path, method) in listOf("/api/v1/sample/events" to "get", "/api/v1/sample/notes" to "post")) {
            val operation = spec.path("paths").path(path).path(method)
            assertTrue(!operation.isMissingNode, path)
            assertTrue(operation.path("security").isMissingNode, "inherits the session requirement: $path")
            assertTrue(operation.path("responses").has("401"), path)
        }
    }

    @Test
    fun `Слушатель пишет событие входа в sample_events`() {
        jdbc.update("delete from sample_events")
        admin()

        val types = jdbc.queryForList("select type from sample_events", String::class.java)

        assertTrue("signin.succeeded" in types, types.toString())
    }

    @Test
    fun `Без свойства субъекта образец даёт actor субъекта сессии ядра`() {
        val session = admin()
        jdbc.update("delete from sample_events")

        val body = """{"backupTimeoutMinutes":481,"restoreTimeoutMinutes":601}"""
        assertEquals(200, client.send("PUT", "/api/v1/run-defaults", session, body).status)

        val detail =
            jdbc.queryForList("select detail from sample_events where type = 'settings.changed'", String::class.java).single().orEmpty()
        assertTrue("actor=administrator:admin" in detail, detail)
    }

    @Test
    fun `Лицензия, что бы в ней ни было, не останавливает работу сервера`() {
        assertEquals(200, client.send("GET", "/api/v1/sources", admin()).status)
    }
}
