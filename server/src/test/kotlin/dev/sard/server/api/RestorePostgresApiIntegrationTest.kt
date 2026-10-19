// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import dev.sard.proto.agent.v1.Action
import dev.sard.proto.agent.v1.RestoreOutput
import dev.sard.proto.agent.v1.StepPhase
import dev.sard.proto.agent.v1.StepStatus
import dev.sard.server.agents.dispatch.ReconciledHellos
import dev.sard.server.enrollment.Enrollment
import dev.sard.server.enrollment.EnrollmentTokens
import dev.sard.server.pki.CertificateAuthority
import dev.sard.server.pki.MovableClock
import dev.sard.server.registration.Registration
import dev.sard.server.registration.RepositoryEntry
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.beans.factory.annotation.Value
import org.springframework.boot.test.web.server.LocalServerPort
import org.springframework.jdbc.core.JdbcTemplate
import tools.jackson.databind.JsonNode
import tools.jackson.databind.ObjectMapper
import java.time.Instant
import kotlin.test.AfterTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

private val SQ = "5".repeat(64)
private val SQ_AT: Instant = Instant.parse("2026-10-11T03:00:00Z")
private const val PG_BACKUP =
    """{"host":"DB1","port":5432,"database":"app","user":"backup","password_ref":"pg-app","tls_mode":"disable"}"""
private const val RESTORED =
    """{"host":"other-db","user":"restorer","password_ref":"pg-restore","database":"app_restored_20261011"}"""

/** R2b: the restore of a PostgreSQL database over REST and gRPC (docs/specs/server/console-restore.feature). */
@RestApiTest
class RestorePostgresApiIntegrationTest(
    @Autowired ca: CertificateAuthority,
    @Autowired enrollment: Enrollment,
    @Autowired tokens: EnrollmentTokens,
    @Autowired registration: Registration,
    @Autowired jdbc: JdbcTemplate,
    @Autowired clock: MovableClock,
    @Autowired private val mapper: ObjectMapper,
    @Autowired private val hellos: ReconciledHellos,
    @LocalServerPort port: Int,
    @Value("\${local.grpc.server.port}") grpc: Int,
) {
    private val world = RestWorld(ca, enrollment, tokens, registration, jdbc, clock, mapper, port, grpc)
    private val tenant = world.tenant()
    private val admin = world.admin(tenant)
    private val w = RestoreWorld(world, tenant, admin)
    private val q = w.source("Q", w.a, plugin = "postgresql", config = PG_BACKUP)

    init {
        w.snapshot(q, SQ, at = SQ_AT)
    }

    @AfterTest
    fun `drop the tenants`() = world.close()

    private fun body(
        agent: Any?,
        config: String = RESTORED,
    ) = restoreBody(agent, config)

    private fun preview(
        agent: Any?,
        config: String = RESTORED,
    ) = w.preview(q, body(agent, config), SQ)

    private fun warnings(response: ApiResponse): List<String> =
        response.json
            .path("warnings")
            .list()
            .map { it.asString() }

    @Test
    fun `Снимок источника PostgreSQL отмечен как восстановимый`() {
        val snapshot =
            world.api
                .get("/api/v1/sources/$q/snapshots", admin)
                .json
                .path("items")
                .get(0)

        assertEquals(true, snapshot.path("restorable").asBoolean())
        assertTrue(snapshot.path("restoreBlockedReason").isNull)
    }

    @Test
    fun `Агент без схемы восстановления postgresql назван устаревшим`() {
        val copy = listOf(RepositoryEntry("copy", "local", RID, ""))
        w.reRegister(w.b, copy, listOf(filesPlugin(), postgresqlPlugin("")))

        val options = w.options(q, SQ).json

        assertEquals(listOf("db1"), options.pluck("targets", "hostname"))
        assertEquals(listOf("agent_outdated"), options.pluck("ineligible", "reason"))
        assertEquals(listOf("db2"), options.pluck("ineligible", "hostname"))
    }

    @Test
    fun `Цель восстановления базы не называет каталог восстановления`() {
        val target =
            w
                .options(q, SQ)
                .json
                .path("targets")
                .get(0)

        assertTrue(target.path("restoreDir").isNull)
    }

    @Test
    fun `Конфиг по умолчанию берёт подключение из бэкапа и даёт новое имя базы`() {
        val target =
            w
                .options(q, SQ)
                .json
                .path("targets")
                .get(0)

        assertEquals("db1", target.path("hostname").asString())
        val defaults = target.path("defaults")
        assertEquals(
            mapper.readTree(
                """{"host":"DB1","port":5432,"user":"backup","password_ref":"pg-app","tls_mode":"disable",""" +
                    """"database":"app_restored_20261011","restore_globals":false}""",
            ),
            defaults,
        )
        assertTrue("restore_globals" in target.path("restoreConfigSchema").toString())
    }

    @Test
    fun `Предпросмотр без глобальных объектов предупреждает о владельцах объектов`() {
        connect()
        val response = preview(w.a.agentId)

        assertEquals(200, response.status)
        assertEquals("app_restored_20261011", response.json.path("target").asString())
        assertTrue(response.json.path("paths").isEmpty)
        assertEquals(false, response.json.path("restoreGlobals").asBoolean())
        assertEquals(listOf("pg_owners_not_restored"), warnings(response))
    }

    @Test
    fun `Выбор глобальных объектов предупреждает об изменении кластера`() {
        connect()
        val config = RESTORED.replace("}", ""","restore_globals":true}""")

        val response = preview(w.a.agentId, config)

        assertEquals(listOf("globals_change_cluster"), warnings(response))
        assertEquals(true, response.json.path("restoreGlobals").asBoolean())
        assertEquals("app_restored_20261011", response.json.path("target").asString())
    }

    @Test
    fun `Предпросмотр предупреждает о восстановлении в кластер источника по признаку сервера`() {
        fun cluster(
            agent: Any,
            host: String,
            port: String?,
        ): Boolean {
            val extra = port?.let { ""","port":$it""" }.orEmpty()
            val config = """{"host":"$host"$extra,"user":"r","password_ref":"pg-restore","database":"x"}"""
            return "pg_same_cluster" in warnings(preview(agent, config))
        }

        assertTrue(cluster(w.a.agentId, "db1", "5432"))
        assertTrue(cluster(w.a.agentId, "DB1", null))
        assertTrue(!cluster(w.a.agentId, "db1", "5433"))
        assertTrue(!cluster(w.a.agentId, "db9", "5432"))
        assertTrue(!cluster(w.b.agentId, "db1", "5432"))
    }

    @Test
    fun `Предупреждение о кластере источника не мешает запуску`() {
        connect()
        val config = """{"host":"db1","user":"r","password_ref":"pg-restore","database":"x"}"""
        assertTrue("pg_same_cluster" in warnings(preview(w.a.agentId, config)))

        val response = w.restore(q, body(w.a.agentId, config), SQ)

        assertEquals(201, response.status)
    }

    @Test
    fun `Секрет, которого нет на целевом агенте, отклоняется`() {
        val config = RESTORED.replace("pg-restore", "only-on-a")

        val response = w.restore(q, body(w.b.agentId, config), SQ)

        assertEquals(422, response.status)
        assertEquals("validation_failed", response.code)
        assertEquals(listOf("config/password_ref"), response.errorFields())
        assertEquals(0, w.restoreRuns())
    }

    @Test
    fun `Пароль базы в запросе не принимается и нигде не остаётся`() {
        val config = RESTORED.replace("}", ""","password":"hunter2-s3cret"}""")

        val response = w.restore(q, body(w.a.agentId, config), SQ)

        assertEquals(422, response.status)
        assertEquals(listOf("config"), response.errorFields())
        assertTrue("hunter2-s3cret" !in response.toString())
        val stored = world.jdbc.queryForList("select config::text from run_steps where config::text like '%hunter2%'")
        assertTrue(stored.isEmpty())
    }

    // --- the run as the agent reports it

    private fun connect() = FakeAgent(world.connect(w.a).also { it.hello() }).also { hellos.await(w.a.agentId) }

    private fun stepOf(run: String): JsonNode =
        world.api
            .get("/api/v1/runs/$run", admin)
            .json
            .path("steps")
            .get(0)

    @Test
    fun `Запуск восстановления PostgreSQL хранит базу как цель и отправляет агенту конфиг запроса`() {
        val fake = connect()

        val response = w.restore(q, body(w.a.agentId), SQ)

        assertEquals(201, response.status)
        assertEquals(
            "app_restored_20261011",
            response.json
                .path("restore")
                .path("target")
                .asString(),
        )
        val command = fake.nextRunStep()
        assertEquals(Action.ACTION_RESTORE, command.action)
        val named = listOf(command.plugin, command.repositoryName, command.snapshotId)
        assertEquals(listOf("postgresql", "main", SQ), named)
        assertEquals(mapper.readTree(RESTORED), mapper.readTree(command.configJson))
        assertTrue("hunter2" !in command.toString())
    }

    @Test
    fun `Итог PostgreSQL называет базу и число таблиц, и не называет файлы`() {
        val fake = connect()
        val response = w.restore(q, body(w.a.agentId), SQ)
        val run = response.json.path("id").asString()
        val command = fake.nextRunStep()
        fake.progress(command.commandId, StepPhase.STEP_PHASE_RESTORING)
        val output =
            RestoreOutput
                .newBuilder()
                .setTarget("app_restored_20261011")
                .setDatabase("app_restored_20261011")
                .setTablesRestored(12)
                .build()

        fake.result(command.commandId, StepStatus.STEP_STATUS_SUCCEEDED, restore = output)

        eventually {
            assertEquals(
                "succeeded",
                world.api
                    .get("/api/v1/runs/$run", admin)
                    .json
                    .path("status")
                    .asString(),
            )
        }
        val step = stepOf(run)
        val restore = step.path("restore")
        assertEquals("app_restored_20261011", restore.path("database").asString())
        assertEquals(listOf(12L, 0L), listOf("tablesRestored", "rolesRestored").map { restore.path(it).asLong() })
        assertTrue(restore.path("leftover").isNull)
        assertTrue(listOf("filesRestored", "bytesRestored", "ownersRestored").all { restore.path(it).isNull })
        assertEquals("restored", step.path("restoreOutcome").asString())
    }

    @Test
    fun `Исход шага восстановления базы`() {
        val fake = connect()

        fun run(
            status: StepStatus,
            message: String,
            output: RestoreOutput? = null,
        ): JsonNode {
            val run =
                w
                    .restore(q, body(w.a.agentId), SQ)
                    .json
                    .path("id")
                    .asString()
            val command = fake.nextRunStep()
            fake.result(command.commandId, status, message, restore = output)
            eventually {
                assertTrue(
                    world.api
                        .get("/api/v1/runs/$run", admin)
                        .json
                        .path("status")
                        .asString() != "queued",
                )
            }
            return eventually { stepOf(run).also { assertTrue(it.path("restoreOutcome").asString() != "") } }
        }

        val failed = run(StepStatus.STEP_STATUS_FAILED, "PG_RESTORE_FAILED (restore): …; nothing was restored")
        val left =
            run(
                StepStatus.STEP_STATUS_FAILED,
                "CLEANUP_FAILED (rollback): …",
                RestoreOutput.newBuilder().setLeftover("database app_restored_20261011").build(),
            )

        assertEquals("nothing", failed.path("restoreOutcome").asString())
        assertEquals("rollback_failed", left.path("restoreOutcome").asString())
        assertEquals("database app_restored_20261011", left.path("restore").path("leftover").asString())
    }
}
