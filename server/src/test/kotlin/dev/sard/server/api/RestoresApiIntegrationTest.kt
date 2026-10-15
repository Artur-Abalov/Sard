// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import dev.sard.server.agents.dispatch.ReconciledHellos
import dev.sard.server.enrollment.Enrollment
import dev.sard.server.enrollment.EnrollmentTokens
import dev.sard.server.pki.CertificateAuthority
import dev.sard.server.pki.MovableClock
import dev.sard.server.registration.PluginAction
import dev.sard.server.registration.Registration
import dev.sard.server.registration.RepositoryEntry
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.beans.factory.annotation.Value
import org.springframework.boot.test.web.server.LocalServerPort
import org.springframework.jdbc.core.JdbcTemplate
import tools.jackson.databind.ObjectMapper
import kotlin.test.AfterTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

private const val ATTACH =
    "sudo sard-agent repo add main <адрес хранилища> --password-from-file <файл с копией пароля>"
private const val TARGET_SF = "/var/lib/sard-agent/restore/etc%20data/20261010T030000Z"

/** R2a (docs/specs/server/console-restore.feature): which snapshots and agents, the request, the run it makes. */
@RestApiTest
class RestoresApiIntegrationTest(
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
    private val source = w.source()

    @AfterTest
    fun `drop the tenants`() = world.close()

    // --- Восстановить можно только снимок, который сделал Sard и который полон

    @Test
    fun `Снимок успешного запуска отмечен как восстановимый`() {
        w.snapshot(source)

        val snapshot =
            world.api
                .get("/api/v1/sources/$source/snapshots", admin)
                .json
                .path("items")
                .get(0)

        assertEquals(true, snapshot.path("restorable").asBoolean())
        assertTrue(snapshot.path("restoreBlockedReason").isNull)
    }

    @Test
    fun `Неполный снимок не восстанавливается из консоли`() {
        w.snapshot(source, partial = true)

        val snapshot =
            world.api
                .get("/api/v1/sources/$source/snapshots", admin)
                .json
                .path("items")
                .get(0)

        assertEquals(false, snapshot.path("restorable").asBoolean())
        assertEquals("partial", snapshot.path("restoreBlockedReason").asString())
    }

    @Test
    fun `Снимок, удалённый из репозитория, не восстанавливается из консоли`() {
        w.snapshot(source, forgotten = true)

        val snapshot =
            world.api
                .get("/api/v1/sources/$source/snapshots", admin)
                .json
                .path("items")
                .get(0)

        assertEquals("forgotten", snapshot.path("restoreBlockedReason").asString())
    }

    @Test
    fun `Запрос восстановления неполного снимка отклоняется`() {
        w.snapshot(source, partial = true)

        val response = w.restore(source, restoreBody(w.a.agentId))

        assertEquals(422, response.status)
        assertEquals("snapshot_not_restorable", response.code)
        assertEquals(0, w.restoreRuns())
    }

    @Test
    fun `Снимок, которого сервер не знает, не восстанавливается`() {
        assertEquals(404, w.options(source, "c".repeat(64)).status)
        w.snapshot(source)
        assertEquals(404, w.options(source, "c".repeat(64)).status)
    }

    @Test
    fun `Снимок другого источника по пути этого источника не находится`() {
        w.snapshot(source)
        val other = w.source("other", w.a)
        w.snapshot(other, "d".repeat(64))

        assertEquals(404, w.options(source, "d".repeat(64)).status)
        assertEquals(200, w.options(other, "d".repeat(64)).status)
    }

    @Test
    fun `Снимок источника другого плагина не восстанавливается из консоли`() {
        val mysql = w.source("mysql", w.a, plugin = "mysql")
        w.snapshot(mysql, "e".repeat(64))

        val snapshot =
            world.api
                .get("/api/v1/sources/$mysql/snapshots", admin)
                .json
                .path("items")
                .get(0)

        assertEquals(false, snapshot.path("restorable").asBoolean())
        assertEquals("plugin_not_supported", snapshot.path("restoreBlockedReason").asString())
    }

    @Test
    fun `Страница запуска знает, восстановим ли снимок шага`() {
        val (run, _) = w.snapshot(source)

        val step =
            world.api
                .get("/api/v1/runs/$run", admin)
                .json
                .path("steps")
                .get(0)

        assertEquals(true, step.path("backup").path("restorable").asBoolean())
    }

    @Test
    fun `Страница запуска не предлагает восстановить неполный или удалённый из репозитория снимок`() {
        val (partial, _) = w.snapshot(source, "1".repeat(64), partial = true)
        val other = w.source("other")
        val (forgotten, _) = w.snapshot(other, "2".repeat(64), forgotten = true)

        for (run in listOf(partial, forgotten)) {
            val step =
                world.api
                    .get("/api/v1/runs/$run", admin)
                    .json
                    .path("steps")
                    .get(0)
            assertEquals(false, step.path("backup").path("restorable").asBoolean(), run)
        }
    }

    @Test
    fun `Снимок удалённого источника восстанавливается`() {
        w.snapshot(source)
        assertEquals(204, world.api.send("DELETE", "/api/v1/sources/$source", admin).status)

        val response = w.restore(source, restoreBody(w.a.agentId))

        assertEquals(201, response.status)
        assertEquals("restore", response.json.path("kind").asString())
        assertEquals(true, response.json.path("sourceDeleted").asBoolean())
    }

    // --- Целью становится агент с тем же репозиторием

    @Test
    fun `Исходный агент и агент с тем же repositoryId - цели, исходный первым`() {
        w.snapshot(source)

        val options = w.options(source).json

        val targets = options.path("targets").list()
        assertEquals(listOf("db1", "db2"), targets.map { it.path("hostname").asString() })
        assertEquals(listOf(true, false), targets.map { it.path("sourceAgent").asBoolean() })
        assertEquals(listOf("main", "copy"), targets.map { it.path("repositoryName").asString() })
        assertEquals(
            listOf(w.a.agentId.toString(), w.b.agentId.toString()),
            targets.map { it.path("agentId").asString() },
        )
        assertTrue(options.path("ineligible").isEmpty)
    }

    @Test
    fun `Офлайн-агент остаётся целью с отметкой`() {
        w.snapshot(source)

        val targets =
            w
                .options(source)
                .json
                .path("targets")
                .list()

        assertEquals(listOf(false, false), targets.map { it.path("online").asBoolean() })
    }

    @Test
    fun `Подключённый агент отмечен как онлайн`() {
        w.snapshot(source)
        val connection = world.connect(w.b)
        connection.hello()
        hellos.await(w.b.agentId)

        val targets =
            eventually {
                w
                    .options(source)
                    .json
                    .path("targets")
                    .list()
                    .also { assertTrue(it[1].path("online").asBoolean()) }
            }

        assertFalse(targets[0].path("online").asBoolean())
    }

    @Test
    fun `Отозванный агент не цель`() {
        w.snapshot(source)
        w.revoke(w.b)

        val options = w.options(source).json

        assertEquals(listOf("db1"), options.pluck("targets", "hostname"))
        assertTrue(options.path("ineligible").isEmpty)
    }

    @Test
    fun `Агент без восстановления из консоли назван с причиной`() {
        w.snapshot(source)
        val repositories = listOf(RepositoryEntry("copy", "local", RID, ""))

        w.reRegister(w.b, repositories, listOf(filesPlugin(restoreSchema = "")))
        val outdated = w.options(source).json
        w.reRegister(w.b, repositories, listOf(filesPlugin("mysql")))
        val missing = w.options(source).json

        assertEquals(listOf("db1"), outdated.pluck("targets", "hostname"))
        assertEquals(listOf("agent_outdated"), outdated.pluck("ineligible", "reason"))
        assertEquals(listOf("db2"), outdated.pluck("ineligible", "hostname"))
        assertEquals(listOf("plugin_missing"), missing.pluck("ineligible", "reason"))
        assertEquals(listOf("db1"), missing.pluck("targets", "hostname"))
    }

    @Test
    fun `Цель определяется по последнему Register агента`() {
        w.snapshot(source)
        w.reRegister(w.b, emptyList())

        val options = w.options(source).json

        assertEquals(listOf("db1"), options.pluck("targets", "hostname"))
        assertTrue(options.path("ineligible").isEmpty)
    }

    @Test
    fun `Без подходящих агентов сервер даёт команду подключения репозитория`() {
        w.snapshot(source)
        w.revoke(w.a)
        w.reRegister(w.b, emptyList())

        val options = w.options(source).json

        assertTrue(options.path("targets").isEmpty)
        assertEquals("main", options.path("attachHint").path("repositoryName").asString())
        assertEquals(ATTACH, options.path("attachHint").path("command").asString())
    }

    @Test
    fun `Команда подключения не содержит адреса и учётных данных хранилища`() {
        w.snapshot(source)

        val command =
            w
                .options(source)
                .json
                .path("attachHint")
                .path("command")
                .asString()

        assertEquals(ATTACH, command)
        assertFalse(listOf("s3:", "sftp:", "http", "AWS", "key").any { it in command })
    }

    @Test
    fun `Запрос на агент без репозитория отклоняется`() {
        w.snapshot(source)

        val response = w.restore(source, restoreBody(w.c.agentId))

        assertEquals(409, response.status)
        assertEquals("unknown_repository", response.code)
        assertEquals(0, w.restoreRuns())
    }

    @Test
    fun `Запрос на отозванный агент отклоняется`() {
        w.snapshot(source)
        w.revoke(w.b)

        val response = w.restore(source, restoreBody(w.b.agentId))

        assertEquals(409, response.status)
        assertEquals("agent_revoked", response.code)
    }

    @Test
    fun `Агент-верификатор не цель восстановления`() {
        w.snapshot(source)
        val options = w.options(source).json
        val targets = options.path("targets").list().map { it.path("agentId").asString() }
        val ineligible = options.path("ineligible").list().map { it.path("agentId").asString() }
        val verifier = w.v.agentId.toString()
        assertFalse(verifier in targets, "verifier offered as a target: $targets")
        assertFalse(verifier in ineligible, "verifier listed as ineligible: $ineligible")

        val response = w.restore(source, restoreBody(verifier))
        assertEquals(422, response.status)
        assertEquals("agent_role_mismatch", response.code)
        assertEquals(listOf("agentId"), response.errorFields())
        assertEquals(0, w.restoreRuns())

        val preview = w.preview(source, restoreBody(verifier))
        assertEquals(422, preview.status)
        assertEquals("agent_role_mismatch", preview.code)
        assertEquals(listOf("agentId"), preview.errorFields())
    }

    @Test
    fun `Запрос на агент без схемы восстановления отклоняется`() {
        w.snapshot(source)
        w.reRegister(w.b, listOf(RepositoryEntry("copy", "local", RID, "")), listOf(filesPlugin(restoreSchema = "")))

        val response = w.restore(source, restoreBody(w.b.agentId))

        assertEquals(409, response.status)
        assertEquals("unknown_plugin", response.code)
    }

    @Test
    fun `Неверный agentId - ошибка у поля`() {
        w.snapshot(source)
        val foreign = RestoreWorld(world, world.tenant(), world.admin(world.tenant()))
        val values =
            listOf(
                null,
                "x",
                java.util.UUID
                    .randomUUID()
                    .toString(),
                foreign.a.agentId.toString(),
            )

        for (value in values) {
            val response = w.restore(source, restoreBody(value))

            assertEquals(422, response.status, "$value")
            assertEquals("validation_failed", response.code)
            assertEquals(listOf("agentId"), response.errorFields(), "$value")
        }
    }

    // --- Конфиг по умолчанию и проверка конфига

    @Test
    fun `Назначение по умолчанию - имя источника и время снимка`() {
        w.snapshot(source)

        val a =
            w
                .options(source)
                .json
                .path("targets")
                .get(0)

        assertEquals("""{"destination":"etc%20data/20261010T030000Z"}""", a.path("defaults").toString())
        assertEquals("/var/lib/sard-agent/restore", a.path("restoreDir").asString())
        assertEquals("object", a.path("restoreConfigSchema").path("type").asString())
    }

    @Test
    fun `Опции называют снимок и источник`() {
        w.snapshot(source)

        val snapshot = w.options(source).json.path("snapshot")

        assertEquals(SF, snapshot.path("snapshotId").asString())
        assertEquals(SF_AT.toString(), snapshot.path("createdAt").asString())
        assertEquals(SF_BYTES, snapshot.path("totalBytes").asLong())
        assertEquals("etc data", snapshot.path("sourceName").asString())
        assertEquals(listOf("main", "files"), listOf("repositoryName", "plugin").map { snapshot.path(it).asString() })
    }

    @Test
    fun `Конфиг вне схемы восстановления агента отклоняется`() {
        w.snapshot(source)
        val cases =
            mapOf(
                "{}" to "config/destination",
                """{"destination": "../etc"}""" to "config/destination",
                """{"destination": "s1/x", "paths": ["srv"]}""" to "config/paths/0",
                """{"destination": "s1/x", "overwrite": true}""" to "config",
                """{"destination": "s1/x", "password": "p"}""" to "config",
                """{"destination": ".sard-staging/c1"}""" to "config/destination",
                """{"destination": "/etc"}""" to "config/destination",
            )

        for ((config, field) in cases) {
            val response = w.restore(source, restoreBody(w.a.agentId, config))

            assertEquals(422, response.status, config)
            assertEquals("validation_failed", response.code, config)
            assertTrue(field in response.errorFields(), "$config: ${response.errorFields()}")
        }
        assertEquals(0, w.restoreRuns())
    }

    @Test
    fun `Тело без config - тоже ошибка у destination`() {
        w.snapshot(source)

        val response = w.restore(source, """{"agentId": "${w.a.agentId}"}""")

        assertEquals(422, response.status)
        assertEquals(listOf("config/destination"), response.errorFields())
    }

    @Test
    fun `Предпросмотр называет, что и куда будет восстановлено, и ничего не создаёт`() {
        w.snapshot(source)
        val before = world.count("runs", tenant)

        val preview = w.preview(source, restoreBody(w.a.agentId))

        assertEquals(200, preview.status)
        val json = preview.json
        assertEquals(
            listOf("etc data", SF, SF_AT.toString(), "db1"),
            listOf("sourceName", "snapshotId", "createdAt", "hostname").map {
                json.path(it).asString()
            },
        )
        assertEquals(SF_BYTES, json.path("totalBytes").asLong())
        assertEquals(TARGET_SF, json.path("target").asString())
        assertTrue(json.path("paths").isEmpty)
        assertEquals(false, json.path("restoreGlobals").asBoolean())
        assertTrue("owners_not_restored" in json.pluckStrings("warnings"))
        assertEquals(before, world.count("runs", tenant))
    }

    @Test
    fun `Предпросмотр на офлайн-агент предупреждает об ожидании`() {
        w.snapshot(source)

        val preview = w.preview(source, restoreBody(w.b.agentId)).json

        assertEquals(false, preview.path("agentOnline").asBoolean())
        assertTrue("agent_offline" in preview.pluckStrings("warnings"))
    }

    @Test
    fun `Предпросмотр онлайн-агента не предупреждает об ожидании`() {
        w.snapshot(source)
        world.connect(w.b).hello()
        hellos.await(w.b.agentId)

        val preview =
            eventually {
                w.preview(source, restoreBody(w.b.agentId)).json.also { assertTrue(it.path("agentOnline").asBoolean()) }
            }

        assertFalse("agent_offline" in preview.pluckStrings("warnings"))
    }

    @Test
    fun `Предпросмотр называет выбранные пути`() {
        w.snapshot(source)

        val preview = w.preview(source, restoreBody(w.a.agentId, """{"destination": "x", "paths": ["/etc", "/srv"]}"""))

        assertEquals(listOf("/etc", "/srv"), preview.json.pluckStrings("paths"))
    }

    @Test
    fun `Предпросмотр отклоняет то же, что и запуск`() {
        w.snapshot(source)

        val response = w.preview(source, restoreBody(w.a.agentId, """{"destination": "../etc"}"""))

        assertEquals(422, response.status)
        assertEquals(listOf("config/destination"), response.errorFields())
    }

    @Test
    fun `Предпросмотр неполного снимка отклоняется как запуск`() {
        w.snapshot(source, partial = true)

        val response = w.preview(source, restoreBody(w.a.agentId))

        assertEquals(422, response.status)
        assertEquals("snapshot_not_restorable", response.code)
    }

    // --- Восстановление - запуск особого типа

    @Test
    fun `Запрос создаёт запуск восстановления в очереди`() {
        w.snapshot(source)

        val response = w.restore(source, restoreBody(w.a.agentId))

        assertEquals(201, response.status)
        val run = response.json
        assertEquals(
            listOf("restore", "manual", "queued", T0.toString(), "admin"),
            listOf("kind", "trigger", "status", "queuedAt", "requestedBy").map { run.path(it).asString() },
        )
        assertEquals(
            listOf(w.a.agentId.toString(), source),
            listOf(run.path("agentId").asString(), run.path("sourceId").asString()),
        )
        val restore = run.path("restore")
        assertEquals(SF, restore.path("snapshotId").asString())
        assertEquals(SF_AT.toString(), restore.path("snapshotCreatedAt").asString())
        assertEquals("main", restore.path("repositoryName").asString())
        assertEquals("""{"destination":"etc%20data/20261010T030000Z"}""", restore.path("config").toString())
        assertEquals(TARGET_SF, restore.path("target").asString())
        val step = run.path("steps").single()
        assertEquals(
            listOf("restore", "queued", "files", "main"),
            listOf("action", "status", "plugin", "repositoryName").map {
                step.path(it).asString()
            },
        )
    }

    @Test
    fun `Восстановление на другом агенте называет его репозиторий`() {
        w.snapshot(source)

        val run = w.restore(source, restoreBody(w.b.agentId)).json

        assertEquals(w.b.agentId.toString(), run.path("agentId").asString())
        assertEquals("copy", run.path("restore").path("repositoryName").asString())
        assertEquals(
            "copy",
            run
                .path("steps")
                .get(0)
                .path("repositoryName")
                .asString(),
        )
        assertEquals(
            w.b.agentId.toString(),
            run
                .path("steps")
                .get(0)
                .path("agentId")
                .asString(),
        )
    }

    @Test
    fun `Восстановление не создаёт снимков и не отмечает первый бэкап`() {
        w.snapshot(source)
        world.jdbc.update("update runs set status = 'failed', message = 'x' where tenant_id = ?", tenant)
        val before = world.count("snapshots", tenant)

        w.restore(source, restoreBody(w.a.agentId))

        assertEquals(before, world.count("snapshots", tenant))
        assertEquals(
            0,
            world.api
                .get("/api/v1/runs?kind=backup&status=succeeded", admin)
                .json
                .path("items")
                .size(),
        )
    }

    // --- Восстановление не блокирует бэкапы источника и не блокируется ими

    @Test
    fun `Восстановление запускается при идущем бэкапе источника`() {
        w.snapshot(source)
        assertEquals(201, world.api.post("/api/v1/sources/$source/runs", admin, null).status)

        assertEquals(201, w.restore(source, restoreBody(w.a.agentId)).status)
    }

    @Test
    fun `Бэкап запускается при идущем восстановлении источника`() {
        w.snapshot(source)
        assertEquals(201, w.restore(source, restoreBody(w.a.agentId)).status)

        assertEquals(201, world.api.post("/api/v1/sources/$source/runs", admin, null).status)
    }

    @Test
    fun `Два восстановления одного снимка создаются оба`() {
        w.snapshot(source)

        val first = w.restore(source, restoreBody(w.a.agentId, """{"destination": "one"}"""))
        val second = w.restore(source, restoreBody(w.a.agentId, """{"destination": "two"}"""))

        assertEquals(listOf(201, 201), listOf(first.status, second.status))
        assertTrue(first.json.path("id").asString() != second.json.path("id").asString())
    }

    @Test
    fun `Удаление источника при идущем восстановлении отклоняется`() {
        w.snapshot(source)
        val run =
            w
                .restore(source, restoreBody(w.a.agentId))
                .json
                .path("id")
                .asString()

        val response = world.api.send("DELETE", "/api/v1/sources/$source", admin)

        assertEquals(409, response.status)
        assertEquals("run_active", response.code)
        assertEquals(run, response.json.path("activeRunId").asString())
    }

    // --- Список запусков показывает восстановления с типом

    @Test
    fun `Список запусков различает бэкап и восстановление`() {
        w.snapshot(source)
        val restore =
            w
                .restore(source, restoreBody(w.a.agentId))
                .json
                .path("id")
                .asString()

        val items = w.runs().path("items").list()

        assertEquals("restore", items.first { it.path("id").asString() == restore }.path("kind").asString())
        val backups = items.filter { it.path("kind").asString() == "backup" }
        assertTrue(backups.isNotEmpty())
        assertEquals(listOf("admin"), backups.map { it.path("requestedBy").asString() }.distinct())
        assertEquals("admin", items.first { it.path("id").asString() == restore }.path("requestedBy").asString())
    }

    @Test
    fun `Фильтр по типу оставляет только восстановления`() {
        w.snapshot(source)
        w.restore(source, restoreBody(w.a.agentId))

        val items = w.runs("?kind=restore").path("items").list()

        assertEquals(1, items.size)
        assertEquals(listOf("restore"), items.map { it.path("kind").asString() })
        assertEquals(listOf("backup"), w.runs("?kind=backup").pluck("items", "kind").distinct())
    }

    @Test
    fun `Неизвестный тип в фильтре - ошибка у поля`() {
        val response = world.api.get("/api/v1/runs?kind=verify", admin)

        assertEquals(422, response.status)
        assertEquals("validation_failed", response.code)
        assertEquals(listOf("kind"), response.errorFields())
    }

    @Test
    fun `Карточка агента называет его каталог восстановления`() {
        val card = world.api.get("/api/v1/agents/${w.a.agentId}", admin).json

        assertEquals("/var/lib/sard-agent/restore", card.path("restoreDir").asString())
    }

    @Test
    fun `Агент до R2 без каталога восстановления`() {
        world.register(w.b, snapshotOf(restoreDir = "").copy(hostname = "db2"))

        val card = world.api.get("/api/v1/agents/${w.b.agentId}", admin).json

        assertTrue(card.path("restoreDir").isNull)
    }

    // --- Доступ

    @Test
    fun `Операции восстановления без сессии отвечают 401`() {
        w.snapshot(source)

        val responses =
            listOf(
                w.options(source, session = null),
                w.preview(source, restoreBody(w.a.agentId), session = null),
                w.restore(source, restoreBody(w.a.agentId), session = null),
            )

        assertEquals(listOf(401, 401, 401), responses.map { it.status })
        assertEquals(listOf("unauthenticated"), responses.map { it.code }.distinct())
        assertEquals(0, w.restoreRuns())
    }

    @Test
    fun `Запуск восстановления с чужого Origin отклоняется`() {
        w.snapshot(source)

        val response = w.restore(source, restoreBody(w.a.agentId), headers = mapOf("Origin" to "https://evil.example"))

        assertEquals(403, response.status)
        assertEquals("origin_rejected", response.code)
        assertEquals(0, w.restoreRuns())
    }

    @Test
    fun `Снимок другого тенанта не виден`() {
        w.snapshot(source)
        val other = world.admin(world.tenant())

        val responses =
            listOf(
                w.options(source, session = other),
                w.preview(source, restoreBody(w.a.agentId), session = other),
                w.restore(source, restoreBody(w.a.agentId), session = other),
            )

        assertEquals(listOf(404, 404, 404), responses.map { it.status })
        assertEquals(0, w.restoreRuns())
    }

    @Test
    fun `Идентификатор источника, который не UUID, ничего не называет`() {
        assertEquals(404, w.options("not-a-uuid").status)
    }
}

private fun tools.jackson.databind.JsonNode.pluckStrings(name: String): List<String> =
    path(name)
        .iterator()
        .asSequence()
        .map { it.asString() }
        .toList()
