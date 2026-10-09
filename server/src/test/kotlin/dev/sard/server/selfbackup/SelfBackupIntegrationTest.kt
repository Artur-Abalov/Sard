// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.selfbackup

import dev.sard.server.TestcontainersConfiguration
import dev.sard.server.pki.MovableClock
import dev.sard.server.runs.InvalidConfig
import dev.sard.server.runs.RUNS_NOW
import dev.sard.server.runs.Runs
import dev.sard.server.runs.RunsTestConfiguration
import dev.sard.server.runs.SourceDraft
import dev.sard.server.runs.Sources
import dev.sard.server.runs.SystemRole
import dev.sard.server.runs.SystemSourceProtected
import dev.sard.server.runs.Trigger
import dev.sard.server.runs.UnknownRepository
import dev.sard.server.scheduler.ScheduleDraft
import dev.sard.server.scheduler.Schedules
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.context.annotation.Import
import org.springframework.jdbc.core.JdbcTemplate
import tools.jackson.databind.ObjectMapper
import java.time.Duration
import java.util.UUID
import kotlin.test.AfterTest
import kotlin.test.BeforeTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertFalse
import kotlin.test.assertNull
import kotlin.test.assertTrue

/**
 * F6, checks 1, 5 and 6: binding the self-backup's repository creates and keeps the two system sources,
 * a local repository needs a confirmation, and the system sources refuse everything but their schedule.
 */
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.NONE,
    properties = [
        "spring.grpc.server.port=0",
        "sard.self-backup.timezone=Europe/Moscow",
        "sard.self-backup.database-host=postgres",
        "sard.self-backup.database-name=sard",
    ],
)
@Import(TestcontainersConfiguration::class, RunsTestConfiguration::class)
class SelfBackupIntegrationTest(
    @Autowired private val selfBackups: SelfBackups,
    @Autowired private val sources: Sources,
    @Autowired private val schedules: Schedules,
    @Autowired private val runs: Runs,
    @Autowired private val clock: MovableClock,
    @Autowired private val jdbc: JdbcTemplate,
    @Autowired private val mapper: ObjectMapper,
) {
    private val tenant = SelfBackupTenant(jdbc)

    @BeforeTest
    fun `a tenant with its built-in agent`() {
        clock.now = RUNS_NOW
        tenant.create()
    }

    @AfterTest
    fun `drop the tenant`() {
        tenant.drop()
    }

    @Test
    fun `Привязка создаёт два системных источника с конфигами плана и ночным расписанием`() {
        val state = selfBackups.bind(tenant.id, S3_REPOSITORY, confirmLocalStorage = false)

        assertTrue(state.configured)
        assertEquals(tenant.agentId, state.agentId)
        assertEquals(RepositoryState(S3_REPOSITORY, "s3", S3_ID, local = false), state.repository)
        assertEquals(RUNS_NOW, state.boundAt)
        assertEquals(listOf(SystemRole.SELF_DATABASE, SystemRole.SELF_KEYS), state.sources.map { it.role })
        val database = sources.get(tenant.id, state.sources[0].sourceId)
        assertEquals("Sard: database", database.name)
        assertEquals("postgresql", database.plugin)
        assertEquals(SystemRole.SELF_DATABASE, database.systemRole)
        assertEquals(S3_REPOSITORY, database.repositoryName)
        assertEquals(tenant.agentId, database.agentId)
        val config = mapper.readTree(database.config)
        assertEquals("sard_self", config["user"].asString())
        assertEquals("sard-db", config["password_ref"].asString())
        assertEquals("postgres", config["host"].asString())
        assertFalse(config["include_globals"].asBoolean())
        val keys = sources.get(tenant.id, state.sources[1].sourceId)
        assertEquals("files", keys.plugin)
        assertEquals(SystemRole.SELF_KEYS, keys.systemRole)
        for (source in state.sources) {
            val schedule = schedules.get(tenant.id, source.sourceId)
            assertEquals(
                ScheduleDraft("0 3 * * *", "Europe/Moscow", true),
                schedule?.let {
                    ScheduleDraft(it.cron, it.timezone, it.enabled)
                },
            )
        }
    }

    @Test
    fun `Повторная привязка к тому же репозиторию ничего не меняет`() {
        val first = selfBackups.bind(tenant.id, S3_REPOSITORY, confirmLocalStorage = false)
        clock.now = RUNS_NOW.plus(Duration.ofHours(1))

        val second = selfBackups.bind(tenant.id, S3_REPOSITORY, confirmLocalStorage = false)

        assertEquals(first, second)
        for (source in second.sources) {
            assertEquals(RUNS_NOW, sources.get(tenant.id, source.sourceId).updatedAt)
        }
        assertEquals(2, tenant.count("sources"))
        assertEquals(2, tenant.count("schedules"))
    }

    @Test
    fun `Привязка к другому репозиторию переводит те же источники и сохраняет их расписание`() {
        val first = selfBackups.bind(tenant.id, S3_REPOSITORY, confirmLocalStorage = false)
        val database = first.sources[0].sourceId
        schedules.set(tenant.id, database, ScheduleDraft("15 2 * * *", "UTC", true))
        val moved = RUNS_NOW.plus(Duration.ofDays(1))
        clock.now = moved

        val second = selfBackups.bind(tenant.id, SFTP_REPOSITORY, confirmLocalStorage = false)

        assertEquals(first.sources.map { it.sourceId }, second.sources.map { it.sourceId })
        assertEquals(moved, second.boundAt)
        assertEquals("sftp", second.repository?.backend)
        for (source in second.sources) {
            val view = sources.get(tenant.id, source.sourceId)
            assertEquals(SFTP_REPOSITORY, view.repositoryName)
            assertEquals(moved, view.updatedAt)
        }
        assertEquals("15 2 * * *", schedules.get(tenant.id, database)?.cron)
        assertEquals(2, tenant.count("schedules"))
    }

    @Test
    fun `Новый встроенный агент после отзыва старого получает те же источники`() {
        val first = selfBackups.bind(tenant.id, S3_REPOSITORY, confirmLocalStorage = false)
        tenant.revoke(tenant.agentId)
        val replacement = UUID.randomUUID()
        tenant.insertAgent(replacement)

        val second = selfBackups.bind(tenant.id, S3_REPOSITORY, confirmLocalStorage = false)

        assertEquals(replacement, second.agentId)
        assertEquals(first.sources.map { it.sourceId }, second.sources.map { it.sourceId })
        assertEquals(listOf(replacement, replacement), second.sources.map { it.agentId })
    }

    @Test
    fun `Локальный репозиторий без подтверждения не привязывается`() {
        val refused =
            assertFailsWith<LocalStorageUnconfirmed> {
                selfBackups.bind(tenant.id, LOCAL_REPOSITORY, confirmLocalStorage = false)
            }
        assertEquals(LOCAL_REPOSITORY, refused.repositoryName)
        assertEquals(0, tenant.count("sources"))
        assertFalse(selfBackups.get(tenant.id).configured)
    }

    @Test
    fun `Локальный репозиторий с подтверждением привязан, и состояние помнит, что хранилище локальное`() {
        val state = selfBackups.bind(tenant.id, LOCAL_REPOSITORY, confirmLocalStorage = true)

        assertTrue(state.configured)
        assertEquals(true, state.repository?.local)
        assertEquals(state, selfBackups.get(tenant.id))
    }

    @Test
    fun `Привязка отказывает без встроенного агента, с чужим или неинициализированным репозиторием`() {
        assertFailsWith<UnknownRepository> { selfBackups.bind(tenant.id, "elsewhere", confirmLocalStorage = false) }
        val fresh =
            assertFailsWith<RepositoryNotInitialized> {
                selfBackups.bind(tenant.id, FRESH_REPOSITORY, confirmLocalStorage = false)
            }
        assertEquals(FRESH_REPOSITORY, fresh.repositoryName)
        tenant.revoke(tenant.agentId)
        assertFailsWith<SelfAgentMissing> { selfBackups.bind(tenant.id, S3_REPOSITORY, confirmLocalStorage = false) }
        assertEquals(0, tenant.count("sources"))
        assertEquals(0, tenant.count("self_backups"))
    }

    @Test
    fun `Обычный агент не встроенный - самобэкап на нём не настраивается`() {
        tenant.revoke(tenant.agentId)
        tenant.insertAgent(UUID.randomUUID(), builtin = false)
        assertFailsWith<SelfAgentMissing> { selfBackups.bind(tenant.id, S3_REPOSITORY, confirmLocalStorage = false) }
    }

    @Test
    fun `Без секрета пароля на соседе привязка отказывает как неверный конфиг и ничего не создаёт`() {
        tenant.revoke(tenant.agentId)
        tenant.insertAgent(UUID.randomUUID(), secrets = emptyList())
        val refused =
            assertFailsWith<InvalidConfig> { selfBackups.bind(tenant.id, S3_REPOSITORY, confirmLocalStorage = false) }
        assertEquals(listOf("config/password_ref"), refused.violations.map { it.field })
        assertEquals(0, tenant.count("sources"))
    }

    @Test
    fun `Пользовательский источник с тем же именем не мешает привязке`() {
        val draft = SourceDraft("Sard: database", tenant.agentId, "files", S3_REPOSITORY, """{"paths": ["/srv"]}""")
        sources.create(tenant.id, draft)
        val state = selfBackups.bind(tenant.id, S3_REPOSITORY, confirmLocalStorage = false)
        assertTrue(state.configured)
        assertEquals(3, tenant.count("sources"))
    }

    @Test
    fun `Системный источник нельзя изменить или удалить, а расписание - можно`() {
        val state = selfBackups.bind(tenant.id, S3_REPOSITORY, confirmLocalStorage = false)
        val id = state.sources[1].sourceId
        val draft = SourceDraft("mine", tenant.agentId, "files", S3_REPOSITORY, """{"paths": ["/srv"]}""")

        assertEquals(id, assertFailsWith<SystemSourceProtected> { sources.replace(tenant.id, id, draft) }.sourceId)
        assertEquals(id, assertFailsWith<SystemSourceProtected> { sources.delete(tenant.id, id) }.sourceId)
        assertEquals("Sard: keys and configuration", sources.get(tenant.id, id).name)
        val changed = schedules.set(tenant.id, id, ScheduleDraft("0 4 * * 0", "UTC", false))
        assertFalse(changed.enabled)
    }

    @Test
    fun `Список источников помечает системные`() {
        val draft = SourceDraft("mine", tenant.agentId, "files", S3_REPOSITORY, """{"paths": ["/srv"]}""")
        val user = sources.create(tenant.id, draft)
        selfBackups.bind(tenant.id, S3_REPOSITORY, confirmLocalStorage = false)

        val roles = sources.list(tenant.id, null, null, 10).associate { it.id to it.systemRole }

        assertNull(roles[user.id])
        assertEquals(setOf(SystemRole.SELF_DATABASE, SystemRole.SELF_KEYS), roles.values.filterNotNull().toSet())
    }

    @Test
    fun `Запустить сейчас - два ручных запуска, повтор отдаёт уже идущие`() {
        val state = selfBackups.bind(tenant.id, S3_REPOSITORY, confirmLocalStorage = false)

        val started = selfBackups.runNow(tenant.id)

        assertEquals(state.sources.map { it.sourceId }, started.map { it.sourceId })
        assertEquals(listOf(SystemRole.SELF_DATABASE, SystemRole.SELF_KEYS), started.map { it.role })
        assertTrue(started.all { it.started })
        for (run in started) {
            val view = checkNotNull(runs.get(tenant.id, run.runId))
            assertEquals(Trigger.MANUAL, view.trigger)
            assertEquals(run.sourceId, view.sourceId)
        }
        val again = selfBackups.runNow(tenant.id)
        assertEquals(started.map { it.runId }, again.map { it.runId })
        assertTrue(again.none { it.started })
        assertEquals(2, tenant.count("runs"))
    }

    @Test
    fun `Запустить сейчас без привязки отказывает`() {
        assertFailsWith<SelfBackupNotConfigured> { selfBackups.runNow(tenant.id) }
    }

    @Test
    fun `Без привязки состояние - не настроен, но встроенный агент виден`() {
        val state = selfBackups.get(tenant.id)
        assertFalse(state.configured)
        assertEquals(tenant.agentId, state.agentId)
        assertNull(state.repository)
        assertNull(state.boundAt)
        assertTrue(state.sources.isEmpty())
    }
}
