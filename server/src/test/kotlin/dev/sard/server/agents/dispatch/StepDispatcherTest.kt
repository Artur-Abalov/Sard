// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents.dispatch

import dev.sard.server.agents.stream.STREAM_ENDED
import dev.sard.server.agents.stream.SendResult
import dev.sard.server.agents.stream.StreamCloseReason
import dev.sard.server.pki.MovableClock
import dev.sard.server.role.AgentRole
import dev.sard.server.runs.Action
import dev.sard.server.runs.StepState
import dev.sard.server.runs.StepView
import dev.sard.server.runs.WaitingSteps
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import java.time.Duration
import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue
import dev.sard.proto.agent.v1.Action as ProtoAction

/** The dispatch rules of ADR 0038 and the FXs lost deadline, against fake transitions and sessions. */
@MutFlowTest
class StepDispatcherTest {
    private val clock = MovableClock(DISPATCH_NOW)
    private val links = FakeLinks()
    private val metrics = RecordingDispatchMetrics()

    private fun dispatcher(
        ledger: FakeLedger,
        waiting: WaitingSteps = WaitingSteps(0, 0),
        startGrace: Duration = Duration.ZERO,
    ) = StepDispatcher(ledger, ledger, links, clock, LOST_AFTER, metrics, ledger, startGrace) { waiting }

    /** Steps whose lost deadline the dispatcher's clock judges. */
    private fun ledger(vararg steps: StepView) = FakeLedger(*steps, now = { clock.now })

    private fun online() {
        links.online += AGENT.agentId
        links.sessions += AGENT
    }

    @Test
    fun `a queued step goes to an online agent and is dispatched`() {
        online()
        val ledger = ledger(step(1))

        MutFlow.underTest { dispatcher(ledger).onQueued(TENANT, AGENT.agentId) }

        assertEquals(listOf(1L), links.sentIds())
        assertEquals(StepState.DISPATCHED, ledger.status(1))
    }

    /** Warnings the dispatcher logs while [block] runs, as formatted messages. */
    private fun warnings(block: () -> Unit): List<String> {
        val logger = org.slf4j.LoggerFactory.getLogger(StepDispatcher::class.java) as ch.qos.logback.classic.Logger
        val appender =
            ch.qos.logback.core.read
                .ListAppender<ch.qos.logback.classic.spi.ILoggingEvent>()
        appender.start()
        logger.addAppender(appender)
        try {
            block()
        } finally {
            logger.detachAppender(appender)
            appender.stop()
        }
        return appender.list.filter { it.level == ch.qos.logback.classic.Level.WARN }.map { it.formattedMessage }
    }

    @Test
    fun `a step the agent's role does not allow is never sent, it fails with a warning`() {
        val cases =
            listOf(
                AgentRole.VERIFIER to Action.BACKUP,
                AgentRole.VERIFIER to Action.RESTORE,
                AgentRole.VERIFIER to Action.RUN,
                AgentRole.SOURCE to Action.VERIFY,
            )
        for ((role, action) in cases) {
            online()
            links.sent.clear()
            val ledger = ledger(step(1, action = action))
            ledger.roles[AGENT.agentId] = role

            val logged = warnings { MutFlow.underTest { dispatcher(ledger).onQueued(TENANT, AGENT.agentId) } }

            assertEquals(emptyList(), links.sentIds(), "$role $action")
            assertEquals(StepState.FAILED, ledger.status(1), "$role $action")
            assertTrue(ledger.refusals.getValue(UUID(0, 1)).startsWith("agent role mismatch"), "$role $action")
            val line = logged.single()
            val expected = listOf(UUID(0, 1).toString(), AGENT.agentId.toString(), role.stored, action.stored)
            assertTrue(expected.all { it in line }, "$line lacks one of $expected")
            assertTrue("agent role mismatch" in line, line)
        }
    }

    @Test
    fun `a step the role allows is sent as before, a verifier gets VERIFY`() {
        online()
        val verifies = ledger(step(1, action = Action.VERIFY))
        verifies.roles[AGENT.agentId] = AgentRole.VERIFIER

        MutFlow.underTest { dispatcher(verifies).onQueued(TENANT, AGENT.agentId) }

        assertEquals(listOf(1L), links.sentIds())
        assertEquals(StepState.DISPATCHED, verifies.status(1))
    }

    @Test
    fun `every action a role allows is sent as its own wire action`() {
        val cases =
            listOf(
                AgentRole.SOURCE to (Action.BACKUP to ProtoAction.ACTION_BACKUP),
                AgentRole.SOURCE to (Action.RESTORE to ProtoAction.ACTION_RESTORE),
                AgentRole.SOURCE to (Action.RUN to ProtoAction.ACTION_RUN),
                AgentRole.VERIFIER to (Action.VERIFY to ProtoAction.ACTION_VERIFY),
            )
        for ((role, pair) in cases) {
            val (action, wire) = pair
            online()
            links.sent.clear()
            val ledger = ledger(step(1, action = action))
            ledger.roles[AGENT.agentId] = role

            MutFlow.underTest { dispatcher(ledger).onQueued(TENANT, AGENT.agentId) }

            assertEquals(listOf(wire), links.sent.map { it.action }, "$role $action")
            assertEquals(StepState.DISPATCHED, ledger.status(1), "$role $action")
        }
    }

    @Test
    fun `a refused step does not hold back the allowed steps behind it`() {
        online()
        val ledger = ledger(step(1, action = Action.VERIFY), step(2, action = Action.BACKUP))

        MutFlow.underTest { dispatcher(ledger).onQueued(TENANT, AGENT.agentId) }

        assertEquals(listOf(StepState.FAILED, StepState.DISPATCHED), listOf(ledger.status(1), ledger.status(2)))
        assertEquals(listOf(2L), links.sentIds())
    }

    @Test
    fun `a dispatched step the role does not allow is refused on Hello, not sent again`() {
        online()
        val old = DISPATCH_NOW.minusSeconds(10)
        val ledger = ledger(step(1, StepState.DISPATCHED, dispatchedAt = old, action = Action.BACKUP))
        ledger.roles[AGENT.agentId] = AgentRole.VERIFIER

        MutFlow.underTest { dispatcher(ledger).onHello(AGENT, emptyList()) }

        assertEquals(StepState.FAILED, ledger.status(1))
        assertEquals(emptyList(), links.sentIds())
    }

    @Test
    fun `an offline agent gets nothing, its step stays queued`() {
        val ledger = ledger(step(1))
        MutFlow.underTest { dispatcher(ledger).onQueued(TENANT, AGENT.agentId) }
        assertEquals(emptyList(), links.sentIds())
        assertEquals(StepState.QUEUED, ledger.status(1))
    }

    @Test
    fun `a refused send puts the step back in the queue and stops the round`() {
        for (refusal in listOf(SendResult.QueueFull, SendResult.NotConnected)) {
            online()
            links.answers += refusal
            val ledger = ledger(step(1), step(2))

            MutFlow.underTest { dispatcher(ledger).onQueued(TENANT, AGENT.agentId) }

            val statuses = listOf(ledger.status(1), ledger.status(2))
            assertEquals(listOf(StepState.QUEUED, StepState.QUEUED), statuses, "$refusal")
            assertEquals(listOf("claim 1", "release 1"), ledger.calls, "$refusal")
        }
        assertEquals(2, metrics.refused)
    }

    @Test
    fun `a step another path claimed first is not sent twice`() {
        online()
        val ledger = ledger(step(1), step(2))
        ledger.claim(TENANT, UUID(0, 1))

        MutFlow.underTest { dispatcher(ledger).onQueued(TENANT, AGENT.agentId) }

        assertEquals(listOf(2L), links.sentIds())
    }

    @Test
    fun `a Hello delivers queued steps in creation order`() {
        val ledger = ledger(step(1), step(2), step(3))
        MutFlow.underTest { dispatcher(ledger).onHello(AGENT, emptyList()) }
        assertEquals(listOf(1L, 2L, 3L), links.sentIds())
    }

    @Test
    fun `steps the Hello lists are never sent again`() {
        val earlier = DISPATCH_NOW - Duration.ofSeconds(5)
        val listed = listOf(step(1, StepState.DISPATCHED, earlier), step(2, StepState.RUNNING))
        val ledger = ledger(*listed.toTypedArray())
        val dispatcher = dispatcher(ledger)

        MutFlow.underTest { dispatcher.onHello(AGENT, listed.map { it.id.toString() }) }
        clock.now += LOST_AFTER
        MutFlow.underTest { dispatcher.tick() }

        assertEquals(emptyList(), links.sentIds())
        assertEquals(listOf(StepState.DISPATCHED, StepState.RUNNING), listOf(ledger.status(1), ledger.status(2)))
    }

    @Test
    fun `a dispatched step the Hello does not list is sent again and counted`() {
        val ledger = ledger(step(1, StepState.DISPATCHED, DISPATCH_NOW - Duration.ofSeconds(5)))
        MutFlow.underTest { dispatcher(ledger).onHello(AGENT, emptyList()) }
        assertEquals(listOf(1L), links.sentIds())
        assertEquals(1, metrics.redispatched)
    }

    @Test
    fun `a dispatched step already sent to this session is not sent again`() {
        val ledger = ledger(step(1, StepState.DISPATCHED, DISPATCH_NOW))
        MutFlow.underTest { dispatcher(ledger).onHello(AGENT, emptyList()) }
        assertEquals(emptyList(), links.sentIds())
        assertEquals(0, metrics.redispatched)
    }

    @Test
    fun `a refused resend goes back to the queue too`() {
        links.answers += SendResult.QueueFull
        val ledger = ledger(step(1, StepState.DISPATCHED, DISPATCH_NOW - Duration.ofSeconds(5)), step(2))

        MutFlow.underTest { dispatcher(ledger).onHello(AGENT, emptyList()) }

        assertEquals(listOf(StepState.QUEUED, StepState.QUEUED), listOf(ledger.status(1), ledger.status(2)))
    }

    @Test
    fun `a running step the Hello does not list is lost only once the window has passed`() {
        val ledger = ledger(step(1, StepState.RUNNING))
        val dispatcher = dispatcher(ledger)
        MutFlow.underTest { dispatcher.onHello(AGENT, emptyList()) }

        clock.now = DISPATCH_NOW + LOST_AFTER - Duration.ofMillis(1)
        MutFlow.underTest { dispatcher.tick() }
        assertEquals(StepState.RUNNING, ledger.status(1))

        clock.now = DISPATCH_NOW + LOST_AFTER
        val log = dispatchLog { MutFlow.underTest { dispatcher.tick() } }
        assertEquals(StepState.LOST, ledger.status(1))
        assertEquals(listOf("step ${UUID(0, 1)} lost: its agent did not report it"), log)
        assertEquals(emptyList(), links.sentIds(), "a lost step is never sent again")
    }

    @Test
    fun `a later Hello that lists the step takes it off the watch`() {
        val ledger = ledger(step(1, StepState.RUNNING))
        val dispatcher = dispatcher(ledger)
        MutFlow.underTest { dispatcher.onHello(AGENT, emptyList()) }
        MutFlow.underTest { dispatcher.onHello(AGENT, listOf(UUID(0, 1).toString())) }

        clock.now += LOST_AFTER
        MutFlow.underTest { dispatcher.tick() }

        assertEquals(StepState.RUNNING, ledger.status(1))
    }

    @Test
    fun `a step the window marks once is not marked again`() {
        val ledger = ledger(step(1, StepState.RUNNING))
        val dispatcher = dispatcher(ledger)
        MutFlow.underTest { dispatcher.onHello(AGENT, emptyList()) }
        clock.now += LOST_AFTER
        MutFlow.underTest { dispatcher.tick() }
        MutFlow.underTest { dispatcher.tick() }
        assertEquals(listOf("lost 1 true"), ledger.calls)
    }

    @Test
    fun `a result that closes the step between the check's read and its move wins, nothing is reported`() {
        val ledger = ledger(step(1, StepState.RUNNING))
        val dispatcher = dispatcher(ledger)
        MutFlow.underTest { dispatcher.onHello(AGENT, emptyList()) }
        clock.now += LOST_AFTER
        val read = ledger.overdue(clock.now)
        ledger.steps[0] = ledger.steps[0].copy(status = StepState.SUCCEEDED)
        val racing =
            object : dev.sard.server.runs.LostDeadlines by ledger {
                override fun overdue(now: java.time.Instant) = read
            }
        val raced = StepDispatcher(ledger, racing, links, clock, LOST_AFTER, metrics, ledger) { WaitingSteps(0, 0) }

        val log = dispatchLog { MutFlow.underTest { raced.tick() } }

        assertEquals(listOf("lost 1 false"), ledger.calls)
        assertEquals(StepState.SUCCEEDED, ledger.status(1))
        assertEquals(emptyList(), log)
    }

    // --- FXs: the deadline is set when the session ends, at start, by a Hello; the earliest one holds

    @Test
    fun `an agent back within the start grace keeps its step, a Hello that lists it clears the deadline`() {
        val ledger = ledger(step(1, StepState.RUNNING))
        val dispatcher = dispatcher(ledger, startGrace = START_GRACE)
        MutFlow.underTest { dispatcher.onStart() }

        clock.now = DISPATCH_NOW + Duration.ofSeconds(110)
        MutFlow.underTest { dispatcher.tick() }
        MutFlow.underTest { dispatcher.onHello(AGENT, listOf(UUID(0, 1).toString())) }

        assertEquals(StepState.RUNNING, ledger.status(1))
        assertEquals(null, ledger.deadline(1))
        clock.now = DISPATCH_NOW + Duration.ofHours(1)
        dispatcher.tick()
        assertEquals(StepState.RUNNING, ledger.status(1))
    }

    @Test
    fun `an agent that is not back when the start grace ends loses its step`() {
        val ledger = ledger(step(1, StepState.RUNNING))
        val dispatcher = dispatcher(ledger, startGrace = START_GRACE)
        MutFlow.underTest { dispatcher.onStart() }

        clock.now = DISPATCH_NOW + START_GRACE - Duration.ofMillis(1)
        MutFlow.underTest { dispatcher.tick() }
        assertEquals(StepState.RUNNING, ledger.status(1))

        clock.now = DISPATCH_NOW + START_GRACE
        MutFlow.underTest { dispatcher.tick() }
        assertEquals(StepState.LOST, ledger.status(1))
    }

    @Test
    fun `a start grace shorter than the lost window does not shorten the window`() {
        val ledger = ledger(step(1, StepState.RUNNING))
        val dispatcher = dispatcher(ledger, startGrace = Duration.ofSeconds(10))

        MutFlow.underTest { dispatcher.onStart() }

        assertEquals(DISPATCH_NOW + LOST_AFTER, ledger.deadline(1))
    }

    @Test
    fun `a session that ends gives its dispatched and running steps one window, then they are lost`() {
        val ledger = ledger(step(1, StepState.DISPATCHED, DISPATCH_NOW), step(2, StepState.RUNNING), step(3))
        val dispatcher = dispatcher(ledger)

        MutFlow.underTest { dispatcher.onDisconnected(AGENT, STREAM_ENDED) }
        clock.now += LOST_AFTER - Duration.ofMillis(1)
        MutFlow.underTest { dispatcher.tick() }
        assertEquals(listOf(StepState.DISPATCHED, StepState.RUNNING), listOf(ledger.status(1), ledger.status(2)))

        clock.now += Duration.ofMillis(1)
        val log = dispatchLog { MutFlow.underTest { dispatcher.tick() } }

        assertEquals(listOf(StepState.LOST, StepState.LOST, StepState.QUEUED), (1L..3L).map { ledger.status(it) })
        assertEquals((1L..2L).map { "step ${UUID(0, it)} lost: its agent did not report it" }, log)
    }

    @Test
    fun `every end of a session but the server's own shutdown starts the window`() {
        val reasons = StreamCloseReason.entries.map { it.name } + listOf(STREAM_ENDED, "AGENT_REVOKED")
        for (reason in reasons) {
            val ledger = ledger(step(1, StepState.RUNNING))
            MutFlow.underTest { dispatcher(ledger).onDisconnected(AGENT, reason) }
            val stops = reason == StreamCloseReason.SERVER_SHUTTING_DOWN.name
            val expected = if (stops) null else DISPATCH_NOW + LOST_AFTER
            assertEquals(expected, ledger.deadline(1), reason)
        }
    }

    @Test
    fun `a Hello without the step does not push back the deadline its session's end set`() {
        val ledger = ledger(step(1, StepState.RUNNING))
        val dispatcher = dispatcher(ledger)
        MutFlow.underTest { dispatcher.onDisconnected(AGENT, STREAM_ENDED) }
        clock.now += LOST_AFTER.dividedBy(2)
        MutFlow.underTest { dispatcher.onHello(AGENT, emptyList()) }

        clock.now = DISPATCH_NOW + LOST_AFTER
        MutFlow.underTest { dispatcher.tick() }

        assertEquals(StepState.LOST, ledger.status(1))
    }

    @Test
    fun `Hellos more often than the window, none listing the step, do not postpone its loss`() {
        val ledger = ledger(step(1, StepState.RUNNING))
        val dispatcher = dispatcher(ledger)
        repeat(4) {
            MutFlow.underTest { dispatcher.onHello(AGENT, emptyList()) }
            MutFlow.underTest { dispatcher.tick() }
            clock.now += LOST_AFTER.dividedBy(4)
        }
        assertEquals(DISPATCH_NOW + LOST_AFTER, clock.now)

        MutFlow.underTest { dispatcher.tick() }

        assertEquals(StepState.LOST, ledger.status(1))
    }

    @Test
    fun `the server start gives every step in flight a fresh window, replacing the deadline it had`() {
        val other = UUID(3, 3)
        val ledger = ledger(step(1, StepState.RUNNING), step(2, StepState.DISPATCHED, DISPATCH_NOW, other), step(3))
        ledger.deadlines[UUID(0, 1)] = DISPATCH_NOW - Duration.ofHours(1)
        val dispatcher = dispatcher(ledger)

        MutFlow.underTest { dispatcher.onStart() }

        val fresh = DISPATCH_NOW + LOST_AFTER
        assertEquals(listOf(fresh, fresh, null), (1L..3L).map { ledger.deadline(it) })
        MutFlow.underTest { dispatcher.tick() }
        assertEquals(StepState.RUNNING, ledger.status(1), "a server that was down longer than the window loses nothing")
    }

    @Test
    fun `a Hello that lists the step removes the deadline its session's end set`() {
        val ledger = ledger(step(1, StepState.RUNNING))
        val dispatcher = dispatcher(ledger)
        MutFlow.underTest { dispatcher.onDisconnected(AGENT, STREAM_ENDED) }

        MutFlow.underTest { dispatcher.onHello(AGENT, listOf(UUID(0, 1).toString())) }

        assertEquals(null, ledger.deadline(1))
    }

    @Test
    fun `a failure while setting the deadline at the end of a session is logged, never thrown`() {
        val ledger = ledger()
        val broken =
            object : dev.sard.server.runs.LostDeadlines by ledger {
                override fun expectAll(
                    tenantId: UUID,
                    agentId: UUID,
                    deadline: java.time.Instant,
                ) = error("database down")
            }
        val dispatcher =
            StepDispatcher(ledger, broken, links, clock, LOST_AFTER, metrics, ledger) { WaitingSteps(0, 0) }

        val log = dispatchLog { MutFlow.underTest { dispatcher.onDisconnected(AGENT, STREAM_ENDED) } }

        val cause = "java.lang.IllegalStateException: database down"
        assertEquals(listOf("steps of agent ${AGENT.agentId} have no lost deadline: $cause"), log)
    }

    @Test
    fun `the tick delivers queued steps of online sessions only`() {
        val offline = step(2, agent = UUID(3, 3))
        val ledger = ledger(step(1), offline)
        online()
        links.sessions += AGENT.copy(agentId = offline.agentId)

        MutFlow.underTest { dispatcher(ledger).tick() }

        assertEquals(listOf(1L), links.sentIds())
    }

    @Test
    fun `the tick hands the waiting counts to the metrics`() {
        MutFlow.underTest { dispatcher(ledger(), WaitingSteps(3, 2)).tick() }
        assertEquals(WaitingSteps(3, 2), metrics.waiting)
    }

    @Test
    fun `a failure while queuing is logged, never thrown at the caller whose run is committed`() {
        online()
        val broken =
            object : dev.sard.server.runs.DispatchLedger by ledger() {
                override fun active(
                    tenantId: UUID,
                    agentId: UUID,
                ) = error("database down")
            }
        val dispatcher =
            StepDispatcher(broken, ledger(), links, clock, LOST_AFTER, metrics, ledger()) { WaitingSteps(0, 0) }
        val log = dispatchLog { MutFlow.underTest { dispatcher.onQueued(TENANT, AGENT.agentId) } }
        assertEquals(emptyList(), links.sentIds())
        val cause = "java.lang.IllegalStateException: database down"
        val expected = "queued steps of agent ${AGENT.agentId} wait for the next check: $cause"
        assertEquals(listOf(expected), log)
    }
}
