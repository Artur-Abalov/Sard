// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.selfbackup

import dev.sard.server.TestcontainersConfiguration
import dev.sard.server.completeOnboarding
import dev.sard.server.freshness.FreshnessCheck
import dev.sard.server.pki.MovableClock
import dev.sard.server.runs.RUNS_NOW
import dev.sard.server.runs.RunState
import dev.sard.server.runs.Runs
import dev.sard.server.runs.RunsTestConfiguration
import dev.sard.server.runs.SystemRole
import dev.sard.server.scheduler.ScheduleDraft
import dev.sard.server.scheduler.Schedules
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import org.junit.jupiter.api.Tag
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.context.annotation.Import
import org.springframework.jdbc.core.JdbcTemplate
import java.time.Duration
import java.time.Instant
import java.util.UUID
import kotlin.test.AfterTest
import kotlin.test.BeforeTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNull
import kotlin.test.assertTrue

/**
 * F6, check 4 and the banner: the self-backup's health from its sources' runs, schedules and overdue episodes,
 * and the warnings the console shows on every page.
 */
@Tag("mutflow-isolated")
@MutFlowTest(includeTargets = [SourceStates::class, HealthRules::class])
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.NONE,
    properties = ["spring.grpc.server.port=0"],
)
@Import(TestcontainersConfiguration::class, RunsTestConfiguration::class)
class SelfBackupHealthIntegrationTest(
    @Autowired private val selfBackups: SelfBackups,
    @Autowired private val schedules: Schedules,
    @Autowired private val runs: Runs,
    @Autowired private val freshness: FreshnessCheck,
    @Autowired private val clock: MovableClock,
    @Autowired private val jdbc: JdbcTemplate,
) {
    private val tenant = SelfBackupTenant(jdbc)

    @BeforeTest
    fun `a tenant with its built-in agent`() {
        clock.now = RUNS_NOW
        tenant.create()
        // F6 is about the self-backup alone: the onboarding is not over, so no warning about the key copy (F4b).
        jdbc.update("delete from onboarding_steps where step = 'keys_confirmed'")
    }

    @AfterTest
    fun `drop the tenant`() {
        tenant.drop()
        completeOnboarding(jdbc)
    }

    private fun state(): SelfBackupView = MutFlow.underTest { selfBackups.get(tenant.id) }

    private fun bound(repository: String = S3_REPOSITORY): SelfBackupView =
        selfBackups.bind(tenant.id, repository, confirmLocalStorage = repository == LOCAL_REPOSITORY)

    private fun ran(
        source: UUID,
        status: String,
        at: Instant,
        message: String? = null,
    ): UUID {
        clock.now = at
        val run = runs.start(tenant.id, source).id
        tenant.finish(run, status, at, message)
        return run
    }

    @Test
    fun `Только что привязанный самобэкап в норме и без предупреждений`() {
        bound()

        val state = state()

        assertEquals(SelfBackupHealth.OK, state.health)
        assertEquals(emptyList(), state.warnings)
        assertNull(state.overdueSince)
        for (source in state.sources) {
            assertNull(source.lastSuccessAt)
            assertNull(source.lastFailureAt)
            assertNull(source.lastRun)
        }
    }

    @Test
    fun `Первый бэкап сделан, когда оба системных источника отработали после привязки`() {
        val bound = bound()
        assertFalse(bound.firstBackupDone)
        assertFalse(state().firstBackupDone)

        ran(bound.sources[0].sourceId, "succeeded", RUNS_NOW.plus(Duration.ofHours(1)))
        assertFalse(state().firstBackupDone)

        ran(bound.sources[1].sourceId, "failed", RUNS_NOW.plus(Duration.ofHours(2)))
        assertFalse(state().firstBackupDone)

        ran(bound.sources[1].sourceId, "succeeded", RUNS_NOW.plus(Duration.ofHours(3)))
        assertTrue(state().firstBackupDone)
    }

    @Test
    fun `Без подтверждения копии ключей после завершения онбординга - предупреждение при норме`() {
        completeOnboarding(jdbc)
        bound()

        val state = state()

        assertEquals(SelfBackupHealth.OK, state.health)
        assertEquals(listOf(SelfBackupWarning.KEYS_UNCONFIRMED), state.warnings)
    }

    @Test
    fun `Не настроенный самобэкап так и называется, без предупреждений`() {
        val state = state()
        assertEquals(SelfBackupHealth.NOT_CONFIGURED, state.health)
        assertEquals(emptyList(), state.warnings)
    }

    @Test
    fun `Локальное хранилище - постоянное предупреждение при норме`() {
        bound(LOCAL_REPOSITORY)

        val state = state()

        assertEquals(SelfBackupHealth.OK, state.health)
        assertEquals(listOf(SelfBackupWarning.LOCAL_STORAGE), state.warnings)
    }

    @Test
    fun `Упавший последний запуск - ошибка, успех после неё - снова норма`() {
        val database = bound().sources[0].sourceId
        val failedAt = RUNS_NOW.plus(Duration.ofHours(1))
        val failed = ran(database, "failed", failedAt, "connection refused")

        val failing = state()
        assertEquals(SelfBackupHealth.FAILING, failing.health)
        assertEquals(listOf(SelfBackupWarning.FAILING), failing.warnings)
        val source = failing.sources.single { it.role == SystemRole.SELF_DATABASE }
        assertEquals(failedAt, source.lastFailureAt)
        assertEquals(LastRun(failed, RunState.FAILED, failedAt), source.lastRun)
        assertNull(source.lastSuccessAt)

        val succeededAt = RUNS_NOW.plus(Duration.ofHours(2))
        ran(database, "succeeded", succeededAt)
        val back = state()
        assertEquals(SelfBackupHealth.OK, back.health)
        val again = back.sources.single { it.role == SystemRole.SELF_DATABASE }
        assertEquals(succeededAt, again.lastSuccessAt)
        assertEquals(failedAt, again.lastFailureAt)
    }

    @Test
    fun `Часы за порогом - давно не было успеха, с моментом просрочки`() {
        bound()
        // Bound at RUNS_NOW (10:00Z), nightly at 03:00 in the server's zone: due 1.5 days later at the latest.
        clock.now = RUNS_NOW.plus(Duration.ofDays(3))
        freshness.tick()

        val state = state()

        assertEquals(SelfBackupHealth.OVERDUE, state.health)
        assertEquals(listOf(SelfBackupWarning.OVERDUE), state.warnings)
        val sql = "select min(deadline) from overdue_episodes where tenant_id = ?"
        val earliest = jdbc.queryForObject(sql, java.sql.Timestamp::class.java, tenant.id)
        assertEquals(earliest?.toInstant(), state.overdueSince)
    }

    @Test
    fun `Выключенное расписание - предупреждение`() {
        val keys = bound().sources[1].sourceId
        val schedule = schedules.get(tenant.id, keys)!!
        schedules.set(
            tenant.id,
            keys,
            dev.sard.server.scheduler
                .ScheduleDraft(schedule.cron, schedule.timezone, false),
        )

        val state = state()

        assertEquals(SelfBackupHealth.SCHEDULE_DISABLED, state.health)
        assertEquals(listOf(SelfBackupWarning.SCHEDULE_DISABLED), state.warnings)
    }

    @Test
    fun `Источники на отозванном агенте - агент заменён, это важнее ошибки`() {
        val database = bound().sources[0].sourceId
        ran(database, "failed", RUNS_NOW.plus(Duration.ofHours(1)))
        tenant.revoke(tenant.agentId)
        tenant.insertAgent(UUID.randomUUID())

        val state = state()

        assertEquals(SelfBackupHealth.AGENT_REPLACED, state.health)
        assertEquals(listOf(SelfBackupWarning.AGENT_REPLACED, SelfBackupWarning.FAILING), state.warnings)
    }
}
