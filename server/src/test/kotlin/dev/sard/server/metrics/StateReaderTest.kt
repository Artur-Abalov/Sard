// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.metrics

import dev.sard.server.pki.MovableClock
import dev.sard.server.role.AgentRole
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import java.time.Duration
import java.time.Instant
import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertEquals

private val NOW: Instant = Instant.parse("2027-06-15T12:00:00Z")
private val CA_END: Instant = Instant.parse("2036-10-21T00:00:00Z")

/** The snapshot of a tick is the database's rows put together with what only the server's memory knows. */
@MutFlowTest(includeTargets = [StateReader::class])
class StateReaderTest {
    private val clock = MovableClock(NOW)
    private val a = UUID.randomUUID()
    private val b = UUID.randomUUID()
    private val c = UUID.randomUUID()
    private val verifier = UUID.randomUUID()
    private var online: Set<UUID> = emptySet()
    private var asked: Instant? = null
    private val rows =
        StateRows(
            sources = listOf(SourceSuccess(a, NOW, null)),
            agents =
                listOf(
                    AgentRole.SOURCE to a,
                    AgentRole.SOURCE to b,
                    AgentRole.SOURCE to c,
                    AgentRole.VERIFIER to verifier,
                ),
            runsActive = mapOf((MetricRunKind.BACKUP to dev.sard.server.runs.RunState.QUEUED) to 3L),
            verificationJobs = mapOf("queued" to 4L),
            undelivered = mapOf("failed" to 2L),
            nextCertificateExpiry = NOW.plusSeconds(60),
        )
    private val reader =
        StateReader(
            { since ->
                asked = since
                rows
            },
            { online },
            { CA_END },
            { SelfBackupMetric("failing", NOW.minusSeconds(5)) },
            clock,
        )

    private fun read() = MutFlow.underTest { reader.read() }

    @Test
    fun `Агентов на связи считают по ролям среди известных базе агентов`() {
        online = setOf(a, verifier, UUID.randomUUID())

        val state = read()

        assertEquals(mapOf(AgentRole.SOURCE to 3L, AgentRole.VERIFIER to 1L), state.agents)
        assertEquals(mapOf(AgentRole.SOURCE to 1L, AgentRole.VERIFIER to 1L), state.agentsOnline)
    }

    @Test
    fun `Без агентов на связи роли отдаются нулями или не отдаются вовсе`() {
        val state = read()

        assertEquals(0L, state.agentsOnline[AgentRole.SOURCE] ?: 0L)
        assertEquals(0L, state.agentsOnline[AgentRole.VERIFIER] ?: 0L)
    }

    @Test
    fun `Доставки считаются за последние сутки по часам сервера`() {
        read()

        assertEquals(NOW.minus(Duration.ofHours(24)), asked)
    }

    @Test
    fun `Остальное берётся из строк базы, срока CA и самобэкапа как есть`() {
        val state = read()

        assertEquals(rows.sources, state.sources)
        assertEquals(rows.runsActive, state.runsActive)
        assertEquals(rows.verificationJobs, state.verificationJobs)
        assertEquals(rows.undelivered, state.undelivered)
        assertEquals(rows.nextCertificateExpiry, state.nextCertificateExpiry)
        assertEquals(CA_END, state.caExpiry)
        assertEquals("failing", state.selfBackupHealth)
        assertEquals(NOW.minusSeconds(5), state.selfBackupLastSuccess)
    }
}
