// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.recovery

import dev.sard.server.agents.dispatch.StepDispatcher
import dev.sard.server.completeOnboarding
import dev.sard.server.onboarding.Installation
import dev.sard.server.onboarding.RunningServer
import dev.sard.server.onboarding.StartOptions
import dev.sard.server.pki.CaStartRefusal
import dev.sard.server.pki.CaStartRefused
import dev.sard.server.pki.CertificateAuthority
import dev.sard.server.pki.MovableClock
import dev.sard.server.runs.RUNS_NOW
import dev.sard.server.runs.Runs
import dev.sard.server.runs.RunsTenant
import dev.sard.server.runs.Sources
import dev.sard.server.scheduler.ScheduleDraft
import dev.sard.server.scheduler.ScheduleTables
import dev.sard.server.scheduler.Scheduler
import dev.sard.server.scheduler.Schedules
import org.junit.jupiter.api.io.TempDir
import org.springframework.jdbc.core.JdbcTemplate
import org.springframework.jdbc.datasource.DriverManagerDataSource
import java.nio.file.Path
import java.sql.Timestamp
import java.time.Duration
import java.time.Instant
import java.util.UUID
import kotlin.test.AfterTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNotNull
import kotlin.test.assertTrue

private const val CODE_LINE = "SARD SETUP CODE"
private const val DAILY = "0 3 * * *"
private val DOWNTIME = Duration.ofDays(3)
private val LOST_WINDOW = Duration.ofSeconds(60)

/**
 * Rule "Восстановленная база даёт ту же установку с тем же онбордингом и историей" of
 * docs/specs/server/server-recovery.feature: what an existing server does over a database brought back from a dump
 * (a server started, stopped and started again over the same database and CA directory, after the rows of the history
 * were written). The clock is the test's; the scheduler's passes and the check of lost steps are called, not
 * waited for.
 */
class RestoredDatabaseStartupIntegrationTest {
    @TempDir
    lateinit var tmp: Path

    private val running = mutableListOf<RunningServer>()
    private val installation by lazy { Installation(tmp) }

    @AfterTest
    fun `stop every server`() {
        running.forEach { runCatching { it.close() } }
    }

    private fun start(
        of: Installation = installation,
        clock: MovableClock = MovableClock(RUNS_NOW),
    ): RunningServer =
        of
            .start(StartOptions(clock = clock, properties = mapOf("sard.scheduler.interval" to "1h")))
            .also { running += it }

    private fun stop(server: RunningServer) {
        server.close()
        running -= server
    }

    private fun jdbc(of: Installation = installation): JdbcTemplate {
        val database = of.database
        return JdbcTemplate(DriverManagerDataSource(database.url, database.user, database.password))
    }

    /** A source with a daily schedule and the tenant's agent, as the dump of a working installation holds them. */
    private class Dump(
        val tenant: RunsTenant,
        val source: UUID,
        val schedule: UUID,
    )

    private fun dumpWithSchedule(
        first: RunningServer,
        activeRun: Boolean,
        inFlight: Boolean = false,
    ): Pair<Dump, UUID?> {
        val tenant = RunsTenant(jdbc()).create()
        val source = first.bean(Sources::class.java).create(tenant.id, tenant.draft())
        val schedule = first.bean(Schedules::class.java).set(tenant.id, source.id, ScheduleDraft(DAILY, "UTC", true))
        val run =
            if (activeRun) {
                first
                    .bean(Runs::class.java)
                    .start(tenant.id, source.id)
                    .id
                    .also { if (inFlight) markRunning(it) }
            } else {
                null
            }
        return Dump(tenant, source.id, schedule.id) to run
    }

    /** The step is running on the agent at the moment of the dump. */
    private fun markRunning(run: UUID) {
        val at = Timestamp.from(RUNS_NOW)
        val step = "update run_steps set status = 'running', dispatched_at = ?, started_at = ? where run_id = ?"
        jdbc().update(step, at, at, run)
        jdbc().update("update runs set status = 'running', started_at = ? where id = ?", at, run)
    }

    private fun fingerprintOf(server: RunningServer) = server.bean(CertificateAuthority::class.java).fingerprint().hex

    @Test
    fun `Сервер с CA из снимка и восстановленной базой пишет отпечаток CA как существующего`() {
        val first = start()
        val g = fingerprintOf(first)
        stop(first)

        val second = start()

        val line = second.startLog.map { it.text }.filter { "CA fingerprint=$g" in it }
        assertTrue(line.any { "origin=existing" in it }, second.logText)
    }

    @Test
    fun `Восстановленная база с завершённым онбордингом не печатает код настройки`() {
        val first = start()
        first.client.completeWizard()
        completeOnboarding(jdbc())
        stop(first)

        val second = start()

        assertEquals(
            "none",
            second.client
                .state()
                .json
                .path("restriction")
                .asString(),
        )
        assertFalse(CODE_LINE in second.logText, "a setup code was printed")
    }

    @Test
    fun `CA из другой установки на восстановленной базе останавливает старт`() {
        stop(start())
        val other = Installation(tmp.resolve("other"))
        stop(start(other))
        val mixed = Installation(tmp.resolve("other"), installation.database)

        val (failure, log) = mixed.startFailing(StartOptions(clock = MovableClock(RUNS_NOW)))

        val refused = generateSequence(failure) { it.cause }.filterIsInstance<CaStartRefused>().firstOrNull()
        assertNotNull(refused, failure.toString())
        assertEquals(CaStartRefusal.CA_ORIGIN_NOT_RECORDED, refused.reason)
        assertFalse(log.any { CODE_LINE in it.text }, "a setup code was printed")
    }

    @Test
    fun `Шаг, шедший у отсутствующего агента в момент снимка, становится потерянным через окно от старта`() {
        val first = start()
        val (dump, run) = dumpWithSchedule(first, activeRun = true, inFlight = true)
        stop(first)
        val clock = MovableClock(RUNS_NOW)

        val second = start(clock = clock)
        val dispatcher = second.bean(StepDispatcher::class.java)
        clock.now = RUNS_NOW + LOST_WINDOW.minusSeconds(1)
        dispatcher.tick()
        assertEquals("running", stepStatus(run!!), "lost before the window passed")
        clock.now = RUNS_NOW + LOST_WINDOW
        dispatcher.tick()

        assertEquals("lost", stepStatus(run))
        assertEquals("failed", jdbc().queryForObject("select status from runs where id = ?", String::class.java, run))
        assertNotNull(dump)
    }

    private fun stepStatus(run: UUID): String? {
        val sql = "select status from run_steps where run_id = ?"
        return jdbc().queryForObject(sql, String::class.java, run)
    }

    @Test
    fun `Догоняющее срабатывание источника с активным в дампе запуском пишется как пропуск из-за активного запуска`() {
        val first = start()
        val (dump, run) = dumpWithSchedule(first, activeRun = true)
        stop(first)
        val clock = MovableClock(RUNS_NOW + DOWNTIME)

        val second = start(clock = clock)
        // The first pass finds the downtime and owes the catch-up for its slot; the catch-up itself comes at that slot.
        second.bean(Scheduler::class.java).tick()
        second.bean(Scheduler::class.java).tick()

        val tables = ScheduleTables(jdbc(), dump.tenant)
        val catchUp = tables.fires(dump.schedule).single { it.kind == "catch_up" }
        assertEquals("skipped_active", catchUp.outcome)
        assertEquals(run, catchUp.runId)
        assertEquals(listOf(run), tables.runs().filter { it.sourceId == dump.source }.map { it.id })
    }

    @Test
    fun `Источник без активного запуска после простоя получает ровно один догоняющий запуск`() {
        val first = start()
        val (dump, _) = dumpWithSchedule(first, activeRun = false)
        stop(first)
        val clock = MovableClock(RUNS_NOW + DOWNTIME)

        val second = start(clock = clock)
        second.bean(Scheduler::class.java).tick()
        second.bean(Scheduler::class.java).tick()

        val tables = ScheduleTables(jdbc(), dump.tenant)
        val runs = tables.runs().filter { it.sourceId == dump.source }
        assertEquals(listOf("catch_up"), runs.map { it.trigger })
        val missed =
            jdbc().queryForObject(
                "select missed_count from schedule_fires where schedule_id = ? and outcome = 'skipped_downtime' " +
                    "and catch_up_fire_id is not null",
                Int::class.java,
                dump.schedule,
            )
        assertTrue(missed != null && missed >= 2, "the catch-up stands for $missed missed fires")
        assertEquals(Instant.parse("2026-10-01T03:00:00Z"), tables.fires(dump.schedule).first().scheduledFor)
    }
}
