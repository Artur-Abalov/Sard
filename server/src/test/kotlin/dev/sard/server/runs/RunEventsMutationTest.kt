// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.runs

import dev.sard.server.TestcontainersConfiguration
import dev.sard.server.events.ActorSource
import dev.sard.server.events.SardEventPublisher
import dev.sard.server.extension.RunCreated
import dev.sard.server.extension.RunStarted
import dev.sard.server.extension.RunStatusChanged
import dev.sard.server.extension.SardEvent
import dev.sard.server.extension.Subject
import dev.sard.server.persistence.RunRecord
import dev.sard.server.persistence.TenantSessions
import dev.sard.server.pki.MovableClock
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import org.hibernate.Session
import org.junit.jupiter.api.Tag
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.context.annotation.Import
import org.springframework.jdbc.core.JdbcTemplate
import java.time.Duration
import java.util.UUID
import kotlin.test.AfterTest
import kotlin.test.BeforeTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertNull
import kotlin.test.assertTrue

private val SCHEDULER = Subject("service", "scheduler")
private val LATER = Duration.ofSeconds(5)

/**
 * Mutation tests of the events of a run (CORE1 Р19): created, started and every change of status, published when the
 * transaction has committed, in order, with the fields of the run as the database shows it.
 */
@Tag("mutflow-isolated")
@MutFlowTest(excludeTargets = [TenantSessions::class]) // TenantSessionsTest mutates it
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.NONE,
    properties = ["spring.grpc.server.port=0"],
)
@Import(TestcontainersConfiguration::class, RunsTestConfiguration::class)
class RunEventsMutationTest(
    @Autowired private val sources: Sources,
    @Autowired private val runs: Runs,
    @Autowired private val sessions: TenantSessions,
    @Autowired private val clock: MovableClock,
    @Autowired private val jdbc: JdbcTemplate,
) {
    private val tenant = RunsTenant(jdbc)
    private val published = mutableListOf<SardEvent>()
    private val events = RunEvents(SardEventPublisher { published += it as SardEvent }, clock, { SCHEDULER })

    @BeforeTest
    fun `a tenant with an agent`() {
        clock.now = RUNS_NOW
        tenant.create()
    }

    @AfterTest
    fun `drop the tenant`() = tenant.drop()

    private fun startedRun(name: String = "prod-db"): RunView {
        val source = sources.create(tenant.id, tenant.draft(name))
        return runs.start(tenant.id, source.id)
    }

    private fun setStatus(
        session: Session,
        run: UUID,
        status: String,
    ) = session
        .createNativeQuery("update runs set status = :status where id = :run")
        .setParameter("status", status)
        .setParameter("run", run)
        .executeUpdate()

    private fun <T> inTenant(work: (Session) -> T) = sessions.inTenant(tenant.id, work)

    @Test
    fun `A manual run is created by the administrator, with the fields of the run`() {
        val run = startedRun()
        clock.now = RUNS_NOW + LATER

        inTenant { MutFlow.underTest { events.created(it, checkNotNull(it.find(RunRecord::class.java, run.id))) } }

        val created = published.single() as RunCreated
        assertEquals(RUNS_NOW + LATER, created.occurredAt)
        assertEquals(tenant.id, created.tenantId)
        assertEquals(Subject.ADMINISTRATOR, created.actor)
        assertEquals(run.id, created.runId)
        assertEquals("backup", created.kind)
        assertEquals("manual", created.trigger)
        assertEquals(UUID.fromString(run.sourceId.toString()), created.sourceId)
    }

    @Test
    fun `A run nobody asked for is created by the actor of the thread, a verification run has the kind verification`() {
        val run = startedRun()
        val unasked = "update runs set requested_by = null, trigger = 'verification', kind = 'restore' where id = ?"
        jdbc.update(unasked, run.id)

        inTenant { MutFlow.underTest { events.created(it, checkNotNull(it.find(RunRecord::class.java, run.id))) } }

        val created = published.single() as RunCreated
        assertEquals(SCHEDULER, created.actor)
        assertEquals("verification", created.kind)
        assertEquals("verification", created.trigger)
    }

    @Test
    fun `A restore run has the kind restore`() {
        val run = startedRun()
        jdbc.update("update runs set kind = 'restore' where id = ?", run.id)

        inTenant { MutFlow.underTest { events.created(it, checkNotNull(it.find(RunRecord::class.java, run.id))) } }

        assertEquals("restore", (published.single() as RunCreated).kind)
    }

    @Test
    fun `The event is published when the transaction has committed, not before and not after a rollback`() {
        val run = startedRun()
        var before = -1

        inTenant {
            MutFlow.underTest { events.created(it, checkNotNull(it.find(RunRecord::class.java, run.id))) }
            before = published.size
        }
        val committed = published.size
        assertFailsWith<IllegalStateException> {
            inTenant {
                events.created(it, checkNotNull(it.find(RunRecord::class.java, run.id)))
                error("rolled back")
            }
        }

        assertEquals(0, before)
        assertEquals(1, committed)
        assertEquals(1, published.size)
    }

    @Test
    fun `Starting gives the change of status first and run started after it, with the same fields`() {
        val run = startedRun()
        clock.now = RUNS_NOW + LATER

        val result =
            inTenant { session ->
                MutFlow.underTest {
                    events.movingRun(session, tenant.id, run.id) {
                        setStatus(session, run.id, "running")
                        "moved"
                    }
                }
            }

        assertEquals("moved", result)
        val changed = published[0] as RunStatusChanged
        val started = published[1] as RunStarted
        assertEquals(2, published.size)
        assertEquals(listOf("run.status_changed", "run.started"), published.map { it.type })
        assertEquals("queued", changed.from)
        assertEquals("running", changed.to)
        assertEquals(false, changed.terminal)
        for (event in listOf<SardEvent>(changed, started)) {
            assertEquals(RUNS_NOW + LATER, event.occurredAt)
            assertEquals(tenant.id, event.tenantId)
            assertEquals(Subject.ADMINISTRATOR, event.actor)
        }
        assertEquals(run.id, changed.runId)
        assertEquals(run.id, started.runId)
        assertEquals("backup", changed.kind)
        assertEquals("manual", changed.trigger)
        assertEquals("manual", started.trigger)
        assertEquals("backup", started.kind)
        assertEquals(changed.sourceId, started.sourceId)
    }

    @Test
    fun `Every status other than running gives one change, terminal exactly when the run is no longer active`() {
        val run = startedRun()
        for (state in RunState.entries.filter { it != RunState.QUEUED && it != RunState.RUNNING }) {
            published.clear()
            inTenant { setStatus(it, run.id, "queued") }

            inTenant { session ->
                val move = { setStatus(session, run.id, state.stored) }
                MutFlow.underTest { events.movingRun(session, tenant.id, run.id, move) }
            }

            val change = published.single() as RunStatusChanged
            assertEquals("queued", change.from, state.stored)
            assertEquals(state.stored, change.to)
            assertEquals(!state.active, change.terminal, state.stored)
        }
    }

    @Test
    fun `A scheduled run changes status without an actor`() {
        val run = startedRun()
        jdbc.update("update runs set requested_by = null, trigger = 'schedule' where id = ?", run.id)

        inTenant { session ->
            MutFlow.underTest { events.movingRun(session, tenant.id, run.id) { setStatus(session, run.id, "running") } }
        }

        assertNull(published.first().actor)
        assertEquals("schedule", (published.first() as RunStatusChanged).trigger)
    }

    @Test
    fun `A move that leaves the status as it was publishes nothing`() {
        val run = startedRun()

        inTenant { session ->
            MutFlow.underTest { events.movingRun(session, tenant.id, run.id) { setStatus(session, run.id, "queued") } }
        }

        assertEquals(emptyList(), published)
    }

    @Test
    fun `The run of a step is found by the step, and what the move returns is returned`() {
        val run = startedRun()
        val step = run.steps.single().id

        val result =
            inTenant { session ->
                MutFlow.underTest {
                    events.movingStep(session, tenant.id, step) {
                        setStatus(session, run.id, "failed")
                        42
                    }
                }
            }

        assertEquals(42, result)
        val change = published.single() as RunStatusChanged
        assertEquals(run.id, change.runId)
        assertEquals("queued", change.from)
        assertEquals("failed", change.to)
        assertTrue(change.terminal)
    }

    @Test
    fun `A step or a run that is not there is moved without an event`() {
        val missing = UUID.randomUUID()

        val moved =
            inTenant { session ->
                MutFlow.underTest {
                    val step = events.movingStep(session, tenant.id, missing) { "step" }
                    step + events.movingRun(session, tenant.id, missing) { "run" }
                }
            }

        assertEquals("steprun", moved)
        assertEquals(emptyList(), published)
    }

    @Test
    fun `A run of another tenant is not watched`() {
        val run = startedRun()
        val stranger = UUID.randomUUID()

        val watch = inTenant { session -> MutFlow.underTest { events.watchRun(session, stranger, run.id) } }

        assertNull(watch)
    }

    @Test
    fun `The watch keeps the run, the tenant and the status it had`() {
        val run = startedRun()

        val watch = inTenant { session -> MutFlow.underTest { events.watchRun(session, tenant.id, run.id) } }

        assertEquals(tenant.id, watch?.tenantId)
        assertEquals(run.id, watch?.runId)
        assertEquals("queued", watch?.from)
    }

    @Test
    fun `The changes of several runs are published in the order they were watched`() {
        val first = startedRun("first")
        val second = startedRun("second")

        inTenant { session ->
            val watches = listOf(second, first).mapNotNull { events.watchRun(session, tenant.id, it.id) }
            setStatus(session, first.id, "failed")
            setStatus(session, second.id, "cancelled")
            MutFlow.underTest { events.settled(session, watches) }
        }

        assertEquals(listOf(second.id, first.id), published.map { (it as RunStatusChanged).runId })
    }

    @Test
    fun `A run that is gone gives no event`() {
        inTenant { session ->
            val gone = RunEvents.Watch(tenant.id, UUID.randomUUID(), "queued")
            MutFlow.underTest { events.settled(session, listOf(gone)) }
        }

        assertEquals(emptyList(), published)
    }
}
