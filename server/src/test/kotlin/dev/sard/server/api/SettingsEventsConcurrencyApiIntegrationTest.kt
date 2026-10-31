// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import dev.sard.server.TestAdmin
import dev.sard.server.enrollment.Enrollment
import dev.sard.server.enrollment.EnrollmentTokens
import dev.sard.server.extension.EventLog
import dev.sard.server.extension.RecordingConfiguration
import dev.sard.server.pki.CertificateAuthority
import dev.sard.server.pki.MovableClock
import dev.sard.server.registration.PluginAction
import dev.sard.server.registration.PluginEntry
import dev.sard.server.registration.Registration
import dev.sard.server.registration.RepositoryEntry
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.beans.factory.annotation.Value
import org.springframework.boot.test.web.server.LocalServerPort
import org.springframework.context.annotation.Import
import org.springframework.jdbc.core.JdbcTemplate
import tools.jackson.databind.ObjectMapper
import java.io.File
import java.util.UUID
import java.util.concurrent.CountDownLatch
import java.util.concurrent.Executors
import kotlin.test.AfterTest
import kotlin.test.BeforeTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

private const val RACERS = 6
private const val SCHEDULE_AT_4 = """{"cron":"0 4 * * *","timezone":"UTC","enabled":true}"""
private const val SCHEDULE_AT_5 = """{"cron":"0 5 * * *","timezone":"UTC","enabled":true}"""
private const val VERIFICATION_AT_317 = """{"cron":"17 3 * * *","timezone":"UTC"}"""
private const val REPOSITORY_ID = "0a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f9"
private val POSTGRESQL_SCHEMA: String by lazy { File("../agent/plugins/postgresql/schema.json").readText() }
private val BACKUP = listOf(PluginAction.BACKUP, PluginAction.RESTORE)

private val FAIL_AT_COMMIT =
    """
    create or replace function fail_at_commit() returns trigger language plpgsql as
    ${'$'}${'$'} begin raise exception 'commit refused'; end ${'$'}${'$'};
    create constraint trigger fail_at_commit after insert or update on run_defaults
    deferrable initially deferred for each row execute function fail_at_commit();
    """.trimIndent()

/** Rule "Изменения настроек публикуют событие с видом настройки", Р28: the write's transaction decides. */
@RestApiTest
@Import(RecordingConfiguration::class)
class SettingsEventsConcurrencyApiIntegrationTest(
    @Autowired ca: CertificateAuthority,
    @Autowired enrollment: Enrollment,
    @Autowired tokens: EnrollmentTokens,
    @Autowired registration: Registration,
    @Autowired private val jdbc: JdbcTemplate,
    @Autowired clock: MovableClock,
    @Autowired mapper: ObjectMapper,
    @Autowired private val log: EventLog,
    @LocalServerPort port: Int,
    @Value("\${local.grpc.server.port}") grpc: Int,
) {
    private val world = RestWorld(ca, enrollment, tokens, registration, jdbc, clock, mapper, port, grpc)
    private val tenant = world.tenant()
    private val admin = world.admin(tenant)
    private val agent = world.agent(tenant)
    private val api = world.api

    @BeforeTest
    fun `no events yet`() = log.clear()

    @AfterTest
    fun `drop the tenants`() = world.close()

    private fun defaults(backup: Int) = """{"backupTimeoutMinutes":$backup,"restoreTimeoutMinutes":720}"""

    /** Sends every request at once and returns the statuses. */
    private fun together(requests: List<() -> ApiResponse>): List<Int> {
        val pool = Executors.newFixedThreadPool(requests.size)
        val start = CountDownLatch(1)
        try {
            val futures = requests.map { request -> pool.submit<ApiResponse> { start.await().let { request() } } }
            start.countDown()
            return futures.map { it.get().status }
        } finally {
            pool.shutdown()
        }
    }

    // Сценарий: Два одновременных одинаковых изменения дают одно событие (PUT /api/v1/run-defaults)
    @Test
    fun `identical concurrent run-defaults puts publish one event`() {
        val put = { api.sendUnchecked("PUT", "/api/v1/run-defaults", admin, defaults(401)) }

        val statuses = together(List(RACERS) { put })

        assertEquals(List(RACERS) { 200 }, statuses)
        assertEquals(1, log.settings("run_defaults").size, "${log.events}")
    }

    private fun source(): UUID {
        val created = api.send("POST", "/api/v1/sources", admin, sourceJson("s-${UUID.randomUUID()}", agent.agentId))
        return UUID.fromString(created.json.path("id").asString())
    }

    private fun identical(
        method: String,
        path: String,
        body: String?,
        setting: String,
    ) {
        log.clear()

        val statuses = together(List(RACERS) { { api.sendUnchecked(method, path, admin, body) } })

        assertEquals(List(RACERS) { 200 }, statuses, "$method $path")
        assertEquals(1, log.settings(setting).size, "$setting: ${log.events}")
    }

    // Сценарий: Два одновременных одинаковых изменения дают одно событие (PUT /api/v1/sources/{sourceId})
    @Test
    fun `identical concurrent source puts publish one source_updated`() {
        val id = source()

        identical("PUT", "/api/v1/sources/$id", sourceJson("renamed-$id", agent.agentId), "source_updated")
    }

    // Сценарий: Два одновременных одинаковых изменения дают одно событие (PUT /api/v1/sources/{sourceId}/schedule)
    @Test
    fun `identical concurrent schedule puts publish one source_schedule`() {
        val id = source()

        identical("PUT", "/api/v1/sources/$id/schedule", SCHEDULE_AT_4, "source_schedule")
    }

    // Сценарий: Два одновременных одинаковых изменения дают одно событие (PUT /api/v1/sources/{sourceId}/schedule)
    @Test
    fun `identical concurrent schedule puts over an existing schedule publish one source_schedule`() {
        val id = source()
        api.send("PUT", "/api/v1/sources/$id/schedule", admin, SCHEDULE_AT_5)

        identical("PUT", "/api/v1/sources/$id/schedule", SCHEDULE_AT_4, "source_schedule")
    }

    // Сценарий: Два одновременных одинаковых изменения дают одно событие (PUT /api/v1/sources/{sourceId}/verification)
    @Test
    fun `identical concurrent verification puts publish one source_verification`() {
        val id = source()

        identical("PUT", "/api/v1/sources/$id/verification", """{"enabled":false}""", "source_verification")
    }

    // Сценарий: Два одновременных одинаковых изменения дают одно событие (PUT /api/v1/verification-schedule)
    @Test
    fun `identical concurrent verification-schedule puts publish one verification_schedule`() {
        identical("PUT", "/api/v1/verification-schedule", VERIFICATION_AT_317, "verification_schedule")
    }

    // Сценарий: Два одновременных одинаковых изменения дают одно событие (POST .../revoke токена)
    @Test
    fun `identical concurrent token revokes publish one token_revoked`() {
        val id =
            api
                .send("POST", "/api/v1/enrollment-tokens", admin, "{}")
                .json
                .path("id")
                .asString()

        identical("POST", "/api/v1/enrollment-tokens/$id/revoke", null, "token_revoked")
    }

    // Сценарий: Два одновременных одинаковых изменения дают одно событие (POST .../revoke агента)
    @Test
    fun `identical concurrent agent revokes publish one agent_revoked`() {
        val other = world.agent(tenant, hostname = "to-revoke")

        identical("POST", "/api/v1/agents/${other.agentId}/revoke", null, "agent_revoked")
    }

    // Сценарий: Два одновременных одинаковых изменения дают одно событие (PUT /api/v1/self-backup/repository)
    @Test
    fun `identical concurrent self-backup bindings publish one self_backup_repository`() {
        world.agent(
            tenant,
            snapshotOf(
                plugins = listOf(filesPlugin(), PluginEntry("postgresql", "0.1.0", POSTGRESQL_SCHEMA, BACKUP)),
                repositories = listOf(RepositoryEntry("offsite", "s3", REPOSITORY_ID, "")),
                secretNames = listOf("sard-db"),
            ),
            hostname = "sard-self",
            builtin = true,
        )

        identical("PUT", "/api/v1/self-backup/repository", """{"repositoryName":"offsite"}""", "self_backup_repository")
    }

    // Сценарий: Два одновременных разных изменения одной настройки дают два события
    @Test
    fun `different concurrent run-defaults puts publish an event each`() {
        val statuses =
            together(
                listOf(400, 500).map { backup ->
                    { api.sendUnchecked("PUT", "/api/v1/run-defaults", admin, defaults(backup)) }
                },
            )

        assertEquals(listOf(200, 200), statuses)
        assertEquals(2, log.settings("run_defaults").size, "${log.events}")
        val now =
            api
                .get("/api/v1/run-defaults", admin)
                .json
                .path("backupTimeoutMinutes")
                .asInt()
        assertTrue(now == 400 || now == 500, "$now")
    }

    // Сценарий: Изменение, транзакция которого откатилась после записи, не даёт события
    @Test
    fun `a run-defaults put whose commit fails publishes nothing`() {
        val superuser = TestAdmin.of(jdbc)
        superuser.execute(FAIL_AT_COMMIT)
        try {
            val response = api.sendUnchecked("PUT", "/api/v1/run-defaults", admin, defaults(402))

            assertTrue(response.status !in 200..299, response.toString())
        } finally {
            superuser.execute("drop trigger fail_at_commit on run_defaults")
        }
        assertEquals(
            360,
            api
                .get("/api/v1/run-defaults", admin)
                .json
                .path("backupTimeoutMinutes")
                .asInt(),
        )
        assertEquals(emptyList(), log.settings("run_defaults"))
    }
}
