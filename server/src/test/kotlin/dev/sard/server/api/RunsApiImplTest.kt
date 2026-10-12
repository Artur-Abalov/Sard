// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import dev.sard.server.TestcontainersConfiguration
import dev.sard.server.extension.TenantResolver
import dev.sard.server.pki.MovableClock
import dev.sard.server.runs.RUNS_NOW
import dev.sard.server.runs.Runs
import dev.sard.server.runs.RunsTenant
import dev.sard.server.runs.RunsTestConfiguration
import dev.sard.server.runs.Sources
import dev.sard.server.runs.StepLogs
import dev.sard.server.scheduler.CatchUpPeriods
import dev.sard.server.scheduler.QUIET_LOOP
import dev.sard.server.scheduler.SPACING
import dev.sard.server.scheduler.ScheduleDraft
import dev.sard.server.scheduler.ScheduleTables
import dev.sard.server.scheduler.Scheduler
import dev.sard.server.scheduler.Schedules
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.context.annotation.Import
import org.springframework.jdbc.core.JdbcTemplate
import java.time.Instant
import java.util.UUID
import kotlin.test.AfterTest
import kotlin.test.BeforeTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertNotNull
import kotlin.test.assertNull

private fun at(text: String): Instant = Instant.parse(text)

/** F3b: the run endpoints name the period of a catch-up run, in the list and for one run. */
@MutFlowTest(includeTargets = [RunsApiImpl::class])
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.NONE,
    properties = ["spring.grpc.server.port=0", QUIET_LOOP, SPACING],
)
@Import(TestcontainersConfiguration::class, RunsTestConfiguration::class)
class RunsApiImplTest(
    @Autowired private val sources: Sources,
    @Autowired private val schedules: Schedules,
    @Autowired private val scheduler: Scheduler,
    @Autowired private val runs: Runs,
    @Autowired private val logs: StepLogs,
    @Autowired private val catchUps: CatchUpPeriods,
    @Autowired private val clock: MovableClock,
    @Autowired private val jdbc: JdbcTemplate,
) {
    private val tenant = RunsTenant(jdbc)
    private val tables = ScheduleTables(jdbc, tenant)
    private val api = RunsApiImpl(runs, logs, catchUps, TenantResolver { tenant.id })

    @BeforeTest
    fun `a tenant with an agent`() {
        clock.now = RUNS_NOW
        tenant.create()
    }

    @AfterTest
    fun `drop the tenant`() = tables.drop()

    private fun list(
        source: UUID? = null,
        status: List<RunStatus>? = null,
        from: Instant? = null,
        to: Instant? = null,
        cursor: String? = null,
        limit: Int = 50,
    ) = MutFlow.underTest { api.listRuns(source, null, status, from, to, cursor, limit) }

    private fun get(run: UUID) = MutFlow.underTest { api.getRun(run) }

    /** A catch-up run after a downtime of 11:00-14:00, finished; then a manual run of the same source. */
    private fun catchUpThenManual(): Pair<UUID, UUID> {
        val source = sources.create(tenant.id, tenant.draft("prod-db")).id
        schedules.set(tenant.id, source, ScheduleDraft("0 * * * *", "UTC", enabled = true))
        clock.now = at("2026-09-30T14:30:00Z")
        scheduler.tick()
        clock.now = at("2026-09-30T14:30:02Z")
        scheduler.tick()
        val catchUp = tables.runs().single().id
        tables.finishAll(at("2026-09-30T14:31:00Z"))
        clock.now = at("2026-09-30T15:00:00Z")
        val manual = runs.start(tenant.id, source).id
        return catchUp to manual
    }

    @Test
    fun `a catch-up run names its period in the list and alone, any other run names none`() {
        val (catchUp, manual) = catchUpThenManual()
        val period = CatchUp(at("2026-09-30T11:00:00Z"), at("2026-09-30T14:00:00Z"), 4, false, "UTC")

        val page = list()

        assertEquals(listOf(manual, catchUp), page.items.map { it.id })
        assertEquals(listOf(null, period), page.items.map { it.catchUp })
        assertEquals(listOf(RunTrigger.MANUAL, RunTrigger.CATCH_UP), page.items.map { it.trigger })
        assertEquals(period, get(catchUp).catchUp)
        assertEquals(RunTrigger.CATCH_UP, get(catchUp).trigger)
        assertNull(get(manual).catchUp)
        assertFailsWith<ResourceNotFound> { get(UUID.randomUUID()) }
    }

    @Test
    fun `the list is filtered, paged by cursor and refuses a period that ends before it begins`() {
        val (catchUp, manual) = catchUpThenManual()
        val source = tables.runs().first().sourceId

        assertEquals(listOf(manual), list(status = listOf(RunStatus.QUEUED)).items.map { it.id })
        assertEquals(listOf(catchUp), list(status = listOf(RunStatus.SUCCEEDED)).items.map { it.id })
        assertEquals(2, list(source = source).items.size)
        assertEquals(emptyList(), list(source = UUID.randomUUID()).items)
        assertEquals(listOf(manual), list(from = at("2026-09-30T15:00:00Z")).items.map { it.id })
        assertEquals(listOf(catchUp), list(to = at("2026-09-30T14:59:59Z")).items.map { it.id })

        val first = list(limit = 1)
        assertEquals(listOf(manual), first.items.map { it.id })
        val second = list(cursor = assertNotNull(first.nextCursor), limit = 1)
        assertEquals(listOf(catchUp), second.items.map { it.id })
        assertNull(second.nextCursor)
        assertEquals(listOf(null, null), listOf(list().nextCursor, list(limit = 2).nextCursor))

        val end = at("2026-09-30T15:00:00Z")
        val wrong = assertFailsWith<RequestInvalid> { list(from = end.plusSeconds(1), to = end) }
        assertEquals("queuedFrom", wrong.field)
        // queuedTo is exclusive, so an empty period is not wrong, only empty.
        assertEquals(emptyList(), list(from = at("2026-09-30T15:00:00Z"), to = at("2026-09-30T15:00:00Z")).items)
    }
}
