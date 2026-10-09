// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import dev.sard.server.enrollment.Enrollment
import dev.sard.server.enrollment.EnrollmentTokens
import dev.sard.server.pki.CertificateAuthority
import dev.sard.server.pki.MovableClock
import dev.sard.server.registration.PluginAction
import dev.sard.server.registration.PluginEntry
import dev.sard.server.registration.Registration
import dev.sard.server.registration.RepositoryEntry
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.beans.factory.annotation.Value
import org.springframework.boot.test.web.server.LocalServerPort
import org.springframework.jdbc.core.JdbcTemplate
import tools.jackson.databind.ObjectMapper
import java.io.File
import kotlin.test.AfterTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

private val POSTGRESQL_SCHEMA: String by lazy { File("../agent/plugins/postgresql/schema.json").readText() }
private val BACKUP = listOf(PluginAction.BACKUP, PluginAction.RESTORE)
private const val REPOSITORY_ID = "0a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f9"

/** F6 REST: the self-backup's state, binding its repository, "back up now", and the system sources' guard. */
@RestApiTest
class SelfBackupApiIntegrationTest(
    @Autowired ca: CertificateAuthority,
    @Autowired enrollment: Enrollment,
    @Autowired tokens: EnrollmentTokens,
    @Autowired registration: Registration,
    @Autowired jdbc: JdbcTemplate,
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

    /** The built-in agent of the compose installation, with a repository on S3, one local, one not initialised. */
    private fun selfAgent(secrets: List<String> = listOf("sard-db")) =
        world.agent(
            tenant,
            snapshotOf(
                plugins = listOf(filesPlugin(), PluginEntry("postgresql", "0.1.0", POSTGRESQL_SCHEMA, BACKUP)),
                repositories =
                    listOf(
                        RepositoryEntry("offsite", "s3", REPOSITORY_ID, ""),
                        RepositoryEntry("disk", "local", REPOSITORY_ID.reversed(), ""),
                        RepositoryEntry("fresh", "sftp", "", ""),
                    ),
                secretNames = secrets,
            ),
            hostname = "sard-self",
            builtin = true,
        )

    private fun bind(
        repository: String,
        confirm: Boolean? = null,
    ): ApiResponse {
        val confirmation = confirm?.let { ""","confirmLocalStorage":$it""" }.orEmpty()
        val body = """{"repositoryName":"$repository"$confirmation}"""
        return world.api.send("PUT", "/api/v1/self-backup/repository", admin, body)
    }

    private fun codeOf(response: ApiResponse) = response.json.path("code").asString()

    @Test
    fun `Без привязки состояние - не настроен, со встроенным агентом`() {
        val agent = selfAgent()

        val state = world.api.get("/api/v1/self-backup", admin)

        assertEquals(200, state.status, state.toString())
        assertFalse(state.json.path("configured").asBoolean())
        assertEquals(agent.agentId.toString(), state.json.path("agentId").asString())
        assertTrue(state.json.path("repository").isNull)
        assertEquals(0, state.json.path("sources").size())
    }

    @Test
    fun `Привязка к S3 - два системных источника, видны в списке источников помеченными`() {
        val agent = selfAgent()

        val bound = bind("offsite")

        assertEquals(200, bound.status, bound.toString())
        val json = bound.json
        assertTrue(json.path("configured").asBoolean())
        assertEquals("s3", json.path("repository").path("backend").asString())
        assertEquals(REPOSITORY_ID, json.path("repository").path("repositoryId").asString())
        assertFalse(json.path("repository").path("local").asBoolean())
        assertEquals(
            listOf("self_database", "self_keys"),
            json.path("sources").list().map { it.path("role").asString() },
        )
        val listed =
            world.api
                .get("/api/v1/sources?agentId=${agent.agentId}", admin)
                .json
                .path("items")
        val roles = listed.list().map { it.path("systemRole").asString() }.toSet()
        assertEquals(setOf("self_database", "self_keys"), roles)
        assertEquals(bound.json, world.api.get("/api/v1/self-backup", admin).json)
    }

    @Test
    fun `Локальный репозиторий без подтверждения - 422, с подтверждением - привязан и помечен локальным`() {
        selfAgent()

        val refused = bind("disk")
        assertEquals(422, refused.status, refused.toString())
        assertEquals("local_storage_unconfirmed", codeOf(refused))
        assertEquals(
            "confirmLocalStorage",
            refused.json
                .path("errors")
                .path(0)
                .path("field")
                .asString(),
        )
        assertEquals(422, bind("disk", confirm = false).status)

        val bound = bind("disk", confirm = true)
        assertEquals(200, bound.status, bound.toString())
        assertTrue(
            bound.json
                .path("repository")
                .path("local")
                .asBoolean(),
        )
    }

    @Test
    fun `Привязка отказывает - чужой репозиторий, неинициализированный, без секрета, без встроенного агента`() {
        assertEquals("self_agent_missing", codeOf(bind("offsite")))
        world.agent(tenant, hostname = "db1")
        assertEquals("self_agent_missing", codeOf(bind("offsite")))

        selfAgent(secrets = emptyList())
        val noSecret = bind("offsite")
        assertEquals(422, noSecret.status)
        assertEquals("invalid_config", codeOf(noSecret))
        assertEquals(
            "config/password_ref",
            noSecret.json
                .path("errors")
                .path(0)
                .path("field")
                .asString(),
        )
        val unknown = bind("elsewhere")
        assertEquals("unknown_repository", codeOf(unknown))
        assertEquals(
            "repositoryName",
            unknown.json
                .path("errors")
                .path(0)
                .path("field")
                .asString(),
        )
        assertEquals("repository_not_initialized", codeOf(bind("fresh")))
        assertEquals(422, bind("fresh").status)
    }

    @Test
    fun `Системный источник - 409 на замену и удаление, расписание меняется`() {
        selfAgent()
        val keys =
            bind("offsite")
                .json
                .path("sources")
                .path(1)
                .path("sourceId")
                .asString()
        val body = world.api.get("/api/v1/sources/$keys", admin).json

        val replaced = world.api.send("PUT", "/api/v1/sources/$keys", admin, body.toString())
        assertEquals(409, replaced.status, replaced.toString())
        assertEquals("system_source", codeOf(replaced))
        val deleted = world.api.send("DELETE", "/api/v1/sources/$keys", admin, "")
        assertEquals(409, deleted.status, deleted.toString())
        assertEquals("system_source", codeOf(deleted))

        val schedule =
            world.api.send(
                "PUT",
                "/api/v1/sources/$keys/schedule",
                admin,
                """{"cron":"0 4 * * *","timezone":"UTC","enabled":true}""",
            )
        assertEquals(200, schedule.status, schedule.toString())
    }

    @Test
    fun `Запустить сейчас - запуски обоих источников, повтор отдаёт те же`() {
        selfAgent()
        val sources =
            bind("offsite")
                .json
                .path("sources")
                .list()
                .map { it.path("sourceId").asString() }

        val started = world.api.post("/api/v1/self-backup/runs", admin, "")

        assertEquals(200, started.status, started.toString())
        val runs = started.json.path("runs").list()
        assertEquals(sources, runs.map { it.path("sourceId").asString() })
        assertTrue(runs.all { it.path("started").asBoolean() })
        for (run in runs) {
            val view = world.api.get("/api/v1/runs/${run.path("runId").asString()}", admin)
            assertEquals("manual", view.json.path("trigger").asString())
        }
        val again =
            world.api
                .post("/api/v1/self-backup/runs", admin, "")
                .json
                .path("runs")
                .list()
        assertEquals(runs.map { it.path("runId") }, again.map { it.path("runId") })
        assertTrue(again.none { it.path("started").asBoolean() })
    }

    @Test
    fun `Запустить сейчас без привязки - 409`() {
        selfAgent()
        val refused = world.api.post("/api/v1/self-backup/runs", admin, "")
        assertEquals(409, refused.status, refused.toString())
        assertEquals("self_backup_not_configured", codeOf(refused))
    }
}
