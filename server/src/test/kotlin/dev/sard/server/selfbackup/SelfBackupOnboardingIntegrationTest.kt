// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.selfbackup

import dev.sard.server.TestcontainersConfiguration
import dev.sard.server.completeOnboarding
import dev.sard.server.onboarding.JdbcOnboardingSteps
import dev.sard.server.pki.MovableClock
import dev.sard.server.runs.RUNS_NOW
import dev.sard.server.runs.RunsTestConfiguration
import dev.sard.server.runs.SourceDraft
import dev.sard.server.runs.Sources
import dev.sard.server.runs.SystemRole
import dev.sard.server.scheduler.ScheduleDraft
import dev.sard.server.scheduler.Scheduler
import dev.sard.server.scheduler.Schedules
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.context.annotation.Import
import org.springframework.jdbc.core.JdbcTemplate
import java.time.Duration
import java.time.Instant
import kotlin.test.AfterTest
import kotlin.test.BeforeTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertFalse
import kotlin.test.assertNull
import kotlin.test.assertTrue

private val HOUR: Duration = Duration.ofHours(1)

/**
 * F4b at the level of the component (docs/specs/server/onboarding-self-backup.feature): the first backup of a
 * binding, the confirmation of the key copy, the repositories and the plan the wizard shows.
 */
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.NONE,
    properties = ["spring.grpc.server.port=0", "sard.scheduler.interval=1h"],
)
@Import(TestcontainersConfiguration::class, RunsTestConfiguration::class)
class SelfBackupOnboardingIntegrationTest(
    @Autowired private val selfBackups: SelfBackups,
    @Autowired private val scheduler: Scheduler,
    @Autowired private val schedules: Schedules,
    @Autowired private val sources: Sources,
    @Autowired private val clock: MovableClock,
    @Autowired private val jdbc: JdbcTemplate,
) {
    private val tenant = SelfBackupTenant(jdbc)

    @BeforeTest
    fun `a tenant with its built-in agent`() {
        clock.now = RUNS_NOW
        tenant.create()
        jdbc.update("delete from onboarding_steps where step = 'keys_confirmed'")
    }

    @AfterTest
    fun `drop the tenant, the onboarding is complete again`() {
        tenant.drop()
        completeOnboarding(jdbc)
    }

    private fun bind(
        repository: String = S3_REPOSITORY,
        location: String? = null,
    ): SelfBackupView {
        val local = repository == LOCAL_REPOSITORY
        return selfBackups.bind(tenant.id, repository, confirmLocalStorage = local, location)
    }

    private fun state(): SelfBackupView = selfBackups.get(tenant.id)

    /** "Запустить сейчас"; the runs of [statuses] (database, keys) end at the clock. */
    private fun backUp(
        database: String = "succeeded",
        keys: String = "succeeded",
    ) {
        val runs = selfBackups.runNow(tenant.id)
        val now = clock.instant()
        tenant.finish(runs.single { it.role == SystemRole.SELF_DATABASE }.runId, database, now)
        tenant.finish(runs.single { it.role == SystemRole.SELF_KEYS }.runId, keys, now)
    }

    private fun onboardingCompleted() {
        JdbcOnboardingSteps(jdbc).confirmKeys(clock.instant())
    }

    // ---- Правило: Шаги самобэкапа и копии ключей идут после шага admin и друг за другом ----

    @Test
    fun `Привязка репозитория без успешных бэкапов не выполняет шаг самобэкапа`() {
        val bound = bind()

        assertTrue(bound.configured)
        assertFalse(bound.firstBackupDone)
        assertNull(bound.keysConfirmedAt)
    }

    @Test
    fun `Шаг самобэкапа выполнен, когда оба системных источника успешно забэкапились после привязки`() {
        bind()
        clock.now = RUNS_NOW + HOUR

        backUp()

        assertTrue(state().firstBackupDone)
    }

    @Test
    fun `Успех только одного системного источника не выполняет шаг самобэкапа`() {
        for ((database, keys) in listOf("succeeded" to "failed", "failed" to "succeeded")) {
            bind()
            clock.now = clock.instant() + HOUR

            backUp(database, keys)

            assertFalse(state().firstBackupDone, "$database/$keys")
            tenant.drop()
            tenant.create()
        }
    }

    @Test
    fun `Запуск, который ещё не завершился, первым бэкапом не считается`() {
        bind()

        selfBackups.runNow(tenant.id)

        assertFalse(state().firstBackupDone)
    }

    @Test
    fun `После неудачного первого бэкапа повторный запуск выполняет шаг самобэкапа`() {
        bind()
        clock.now = RUNS_NOW + HOUR
        backUp(database = "failed")
        assertFalse(state().firstBackupDone)

        clock.now = RUNS_NOW + HOUR + HOUR
        backUp()

        assertTrue(state().firstBackupDone)
    }

    @Test
    fun `Успешный запуск до последней привязки не засчитывается`() {
        bind()
        clock.now = RUNS_NOW + HOUR
        backUp()
        assertTrue(state().firstBackupDone)

        clock.now = RUNS_NOW + HOUR + HOUR
        val moved = bind(SFTP_REPOSITORY)

        assertFalse(moved.firstBackupDone)
        assertFalse(state().firstBackupDone)
    }

    @Test
    fun `Повторная привязка того же репозитория не сбрасывает шаг самобэкапа`() {
        bind()
        clock.now = RUNS_NOW + HOUR
        backUp()

        clock.now = RUNS_NOW + HOUR + HOUR
        val again = bind()

        assertTrue(again.firstBackupDone)
    }

    // ---- Правило: Подтверждение копии ключей завершает онбординг только после первых бэкапов ----

    @Test
    fun `Подтверждение копии ключей после первых бэкапов записывает время и репозиторий`() {
        bind()
        clock.now = RUNS_NOW + HOUR
        backUp()

        val confirmed = selfBackups.confirmKeys(tenant.id, clock.instant())

        assertEquals(KeysConfirmed(S3_REPOSITORY, S3_ID, recorded = true), confirmed)
        assertEquals(RUNS_NOW + HOUR, state().keysConfirmedAt)
    }

    @Test
    fun `Подтверждение копии ключей до успешных первых бэкапов отклоняется`() {
        bind()

        assertFailsWith<FirstBackupPending> { selfBackups.confirmKeys(tenant.id, clock.instant()) }

        assertNull(state().keysConfirmedAt)
    }

    @Test
    fun `Подтверждение копии ключей без привязки отклоняется`() {
        assertFailsWith<SelfBackupNotConfigured> { selfBackups.confirmKeys(tenant.id, clock.instant()) }
    }

    @Test
    fun `Повторное подтверждение ничего не меняет`() {
        bind()
        clock.now = RUNS_NOW + HOUR
        backUp()
        selfBackups.confirmKeys(tenant.id, clock.instant())

        val again = selfBackups.confirmKeys(tenant.id, clock.instant() + HOUR)

        assertFalse(again.recorded)
        assertEquals(RUNS_NOW + HOUR, state().keysConfirmedAt)
    }

    // ---- Правило: Смена хранилища после онбординга снова требует первого бэкапа и подтверждения ----

    @Test
    fun `Новая привязка просит подтвердить копию ключей заново и отклоняет подтверждение до первых бэкапов`() {
        bind()
        clock.now = RUNS_NOW + HOUR
        backUp()
        selfBackups.confirmKeys(tenant.id, clock.instant())
        onboardingCompleted()
        assertEquals(emptyList(), state().warnings)

        clock.now = RUNS_NOW + HOUR + HOUR
        val moved = bind(SFTP_REPOSITORY)

        assertNull(moved.keysConfirmedAt)
        assertFalse(moved.firstBackupDone)
        assertEquals(listOf(SelfBackupWarning.KEYS_UNCONFIRMED), moved.warnings)
        assertFailsWith<FirstBackupPending> { selfBackups.confirmKeys(tenant.id, clock.instant()) }
    }

    @Test
    fun `Первые бэкапы и подтверждение для нового хранилища снимают предупреждение`() {
        bind()
        onboardingCompleted()
        clock.now = RUNS_NOW + HOUR
        backUp()
        assertEquals(listOf(SelfBackupWarning.KEYS_UNCONFIRMED), state().warnings)

        selfBackups.confirmKeys(tenant.id, clock.instant())

        assertEquals(emptyList(), state().warnings)
    }

    @Test
    fun `До завершения онбординга предупреждения keys_unconfirmed нет`() {
        bind()
        clock.now = RUNS_NOW + HOUR
        backUp()

        assertEquals(emptyList(), state().warnings)
    }

    @Test
    fun `Повторная привязка того же репозитория после онбординга не просит подтверждения`() {
        bind()
        clock.now = RUNS_NOW + HOUR
        backUp()
        selfBackups.confirmKeys(tenant.id, clock.instant())
        onboardingCompleted()

        val again = bind()

        assertEquals(emptyList(), again.warnings)
        assertEquals(RUNS_NOW + HOUR, again.keysConfirmedAt)
    }

    @Test
    fun `Предупреждение keys_unconfirmed не портит здоровье самобэкапа`() {
        bind()
        onboardingCompleted()

        assertEquals(SelfBackupHealth.OK, state().health)
    }

    // ---- Правило: Мастер видит репозитории соседа и состав самобэкапа ----

    @Test
    fun `Состояние перечисляет репозитории встроенного агента по имени и без привязки`() {
        val view = state()

        assertFalse(view.configured)
        assertEquals(
            listOf(
                RepositoryState(FRESH_REPOSITORY, "s3", null, local = false),
                RepositoryState(LOCAL_REPOSITORY, "local", "f".repeat(64), local = true),
                RepositoryState(S3_REPOSITORY, "s3", S3_ID, local = false),
                RepositoryState(SFTP_REPOSITORY, "sftp", S3_ID.reversed(), local = false),
            ),
            view.repositories,
        )
    }

    @Test
    fun `Без встроенного агента список репозиториев пуст`() {
        tenant.revoke(tenant.agentId)

        assertEquals(emptyList(), state().repositories)
    }

    @Test
    fun `Состав самобэкапа виден до привязки`() {
        val plan = state().plan

        assertEquals(listOf(SystemRole.SELF_DATABASE, SystemRole.SELF_KEYS), plan.map { it.role })
        assertEquals("postgresql", plan[0].plugin)
        assertEquals("files", plan[1].plugin)
    }

    @Test
    fun `Команда показа пароля появляется после привязки`() {
        assertNull(state().passwordRevealCommand)

        val bound = bind()

        val expected =
            "docker compose exec self-agent sard-agent repo password $S3_REPOSITORY --reveal " +
                "--config /etc/sard/self/agent.yaml"
        assertEquals(expected, bound.passwordRevealCommand)
    }

    // ---- Правило: Адрес хранилища без учётных данных сохраняется для карточки ----

    @Test
    fun `Привязка с адресом сохраняет его, без адреса оставляет пустым`() {
        assertNull(bind().location)

        val located = bind(location = "s3:acme-sard/prod")

        assertEquals("s3:acme-sard/prod", located.location)
        assertEquals("s3:acme-sard/prod", state().location)
    }

    @Test
    fun `Повторная привязка без адреса не стирает адрес, а перевод на другое хранилище стирает`() {
        bind(location = "s3:acme-sard/prod")

        assertEquals("s3:acme-sard/prod", bind().location)

        clock.now = RUNS_NOW + HOUR
        assertNull(bind(SFTP_REPOSITORY).location)
    }

    @Test
    fun `Новый адрес для того же репозитория заменяет прежний, не сдвигая момент привязки`() {
        bind(location = "s3:acme-sard/prod")
        clock.now = RUNS_NOW + HOUR

        val again = bind(location = "s3:acme-sard/other")

        assertEquals("s3:acme-sard/other", again.location)
        assertEquals(RUNS_NOW, again.boundAt)
    }

    @Test
    fun `Момент подтверждения не раньше привязки считается подтверждением текущей привязки`() {
        bind()
        clock.now = RUNS_NOW + HOUR
        backUp()
        selfBackups.confirmKeys(tenant.id, clock.instant())
        val sql = "update self_backups set bound_at = ? where tenant_id = ?"
        jdbc.update(sql, java.sql.Timestamp.from(Instant.parse("2026-09-30T11:00:00Z")), tenant.id)

        assertEquals(RUNS_NOW + HOUR, state().keysConfirmedAt)

        jdbc.update(sql, java.sql.Timestamp.from(Instant.parse("2026-09-30T11:00:01Z")), tenant.id)

        assertNull(state().keysConfirmedAt)
    }

    // ---- Расписания не ждут конца онбординга ----

    @Test
    fun `Расписания системных источников срабатывают до завершения онбординга`() {
        val database = bind().sources.single { it.role == SystemRole.SELF_DATABASE }.sourceId
        val due = checkNotNull(schedules.get(tenant.id, database)?.nextRunAt)
        clock.now = due

        scheduler.tick()

        assertEquals(2, tenant.count("runs"))
    }

    @Test
    fun `В обновлённой установке расписания источников администратора срабатывают`() {
        jdbc.update("insert into onboarding_marks (mark, marked_at) values ('upgraded', now()) on conflict do nothing")
        try {
            val draft = SourceDraft("mine", tenant.agentId, "files", S3_REPOSITORY, """{"paths": ["/srv"]}""")
            val mine = sources.create(tenant.id, draft)
            val schedule = schedules.set(tenant.id, mine.id, ScheduleDraft("0 4 * * *", "UTC", true))
            clock.now = checkNotNull(schedule.nextRunAt)

            scheduler.tick()

            assertEquals(1, tenant.count("runs"))
        } finally {
            jdbc.update("delete from onboarding_marks")
        }
    }
}
