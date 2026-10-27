// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.freshness

import dev.sard.server.TestcontainersConfiguration
import dev.sard.server.pki.MovableClock
import dev.sard.server.runs.RUNS_NOW
import dev.sard.server.runs.Runs
import dev.sard.server.runs.RunsTenant
import dev.sard.server.runs.RunsTestConfiguration
import dev.sard.server.runs.Sources
import dev.sard.server.scheduler.ScheduleDraft
import dev.sard.server.scheduler.Schedules
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import org.junit.jupiter.api.Tag
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.context.annotation.Import
import org.springframework.jdbc.core.JdbcTemplate
import java.sql.Timestamp
import java.time.Duration
import java.time.Instant
import java.util.UUID
import kotlin.test.AfterTest
import kotlin.test.BeforeTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNull

/** The schedule is set at RUNS_NOW (10:00Z) to 03:00 daily: its first two fires are 24 h apart, the deadline 36 h. */
private val DEADLINE: Instant = RUNS_NOW.plus(Duration.ofHours(36))
private val NIGHTLY = ScheduleDraft("0 3 * * *", "UTC", true)

/** F6, check 4: an overdue episode opens once, a success closes it, a lost schedule or source ends it quietly. */
@Tag("mutflow-isolated")
@MutFlowTest(includeTargets = [FreshnessCheck::class])
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.NONE,
    properties = ["spring.grpc.server.port=0"],
)
@Import(TestcontainersConfiguration::class, RunsTestConfiguration::class)
class FreshnessCheckIntegrationTest(
    @Autowired private val check: FreshnessCheck,
    @Autowired private val sources: Sources,
    @Autowired private val schedules: Schedules,
    @Autowired private val runs: Runs,
    @Autowired private val clock: MovableClock,
    @Autowired private val jdbc: JdbcTemplate,
) {
    private val tenant = RunsTenant(jdbc)
    private val other = RunsTenant(jdbc)

    @BeforeTest
    fun `two tenants with an agent each`() {
        clock.now = RUNS_NOW
        tenant.create()
        other.create()
    }

    @AfterTest
    fun `drop both tenants`() {
        for (id in listOf(tenant.id, other.id)) {
            jdbc.update("delete from notification_deliveries where tenant_id = ?", id)
            jdbc.update("delete from no_verifier_episodes where tenant_id = ?", id)
            jdbc.update("delete from overdue_episodes where tenant_id = ?", id)
            jdbc.update("delete from schedule_fires where tenant_id = ?", id)
            jdbc.update("delete from schedules where tenant_id = ?", id)
        }
        tenant.drop()
        other.drop()
    }

    private fun scheduled(
        owner: RunsTenant = tenant,
        name: String = "prod-db",
    ): UUID {
        val source = sources.create(owner.id, owner.draft(name = name)).id
        schedules.set(owner.id, source, NIGHTLY)
        return source
    }

    private fun tickAt(at: Instant) {
        clock.now = at
        MutFlow.underTest { check.tick() }
    }

    private fun episodes(source: UUID): List<Map<String, Any?>> =
        jdbc.queryForList("select * from overdue_episodes where source_id = ? order by opened_at", source)

    private fun succeeded(
        source: UUID,
        at: Instant,
    ): UUID {
        clock.now = at
        val run = runs.start(tenant.id, source).id
        tenant.finish(run)
        jdbc.update("update runs set finished_at = ? where id = ?", Timestamp.from(at), run)
        return run
    }

    @Test
    fun `До срока эпизода нет, в срок он открывается один раз`() {
        val source = scheduled()

        tickAt(DEADLINE.minusSeconds(1))
        assertEquals(0, episodes(source).size)

        tickAt(DEADLINE)
        tickAt(DEADLINE.plus(Duration.ofHours(1)))
        val opened = episodes(source).single()
        assertEquals(DEADLINE, (opened["deadline"] as Timestamp).toInstant())
        assertEquals(DEADLINE, (opened["opened_at"] as Timestamp).toInstant())
        assertNull(opened["counted_from"])
        assertNull(opened["closed_at"])
    }

    @Test
    fun `Успех после открытия закрывает эпизод этим запуском`() {
        val source = scheduled()
        tickAt(DEADLINE)
        val success = succeeded(source, DEADLINE.plus(Duration.ofHours(2)))

        tickAt(DEADLINE.plus(Duration.ofHours(3)))

        val closed = episodes(source).single()
        assertEquals(success, closed["closed_by_run_id"])
        assertEquals(DEADLINE.plus(Duration.ofHours(3)), (closed["closed_at"] as Timestamp).toInstant())
    }

    @Test
    fun `После успеха следующий эпизод считается от него`() {
        val source = scheduled()
        val success = Instant.parse("2026-10-01T03:20:00Z")
        succeeded(source, success)

        tickAt(Instant.parse("2026-10-02T15:19:59Z"))
        assertEquals(0, episodes(source).size)
        tickAt(Instant.parse("2026-10-02T15:20:00Z"))
        assertEquals(success, (episodes(source).single()["counted_from"] as Timestamp).toInstant())
    }

    @Test
    fun `Выключенное расписание и удалённый источник закрывают эпизод без запуска`() {
        val disabled = scheduled(name = "disabled")
        val deleted = scheduled(name = "deleted")
        tickAt(DEADLINE)
        schedules.set(tenant.id, disabled, NIGHTLY.copy(enabled = false))
        sources.delete(tenant.id, deleted)

        tickAt(DEADLINE.plus(Duration.ofMinutes(1)))

        for (source in listOf(disabled, deleted)) {
            val closed = episodes(source).single()
            assertEquals(DEADLINE.plus(Duration.ofMinutes(1)), (closed["closed_at"] as Timestamp).toInstant())
            assertNull(closed["closed_by_run_id"])
        }
    }

    @Test
    fun `Расписание, которое больше не разбирается, пропускается, остальные проверяются`() {
        val broken = scheduled(name = "broken")
        val fine = scheduled(name = "fine")
        jdbc.update("update schedules set cron = '61 * * * * *' where source_id = ?", broken)

        tickAt(DEADLINE)

        assertEquals(0, episodes(broken).size)
        assertEquals(1, episodes(fine).size)
    }

    @Test
    fun `Источник без расписания не бывает просрочен, тенанты не смешиваются`() {
        val unscheduled = sources.create(tenant.id, tenant.draft(name = "manual")).id
        val foreign = scheduled(owner = other)

        tickAt(DEADLINE.plus(Duration.ofDays(30)))

        assertEquals(0, episodes(unscheduled).size)
        assertEquals(other.id, episodes(foreign).single()["tenant_id"])
    }
}
