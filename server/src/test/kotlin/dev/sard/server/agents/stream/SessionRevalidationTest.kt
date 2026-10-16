// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents.stream

import dev.sard.server.agents.AgentAuthFailure
import dev.sard.server.agents.BatchStandings
import dev.sard.server.agents.CertificateStanding
import dev.sard.server.agents.stream.StreamFixtures.NOW
import dev.sard.server.agents.stream.StreamFixtures.agent
import dev.sard.server.pki.AgentIdentity
import dev.sard.server.pki.MovableClock
import dev.sard.server.role.AgentRole
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import java.time.Duration
import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertEquals

private fun ConnectedAgent.standing(
    notAfter: java.time.Instant = NOW + Duration.ofDays(1),
    revokedAt: java.time.Instant? = null,
    agentRevokedAt: java.time.Instant? = null,
    role: AgentRole = AgentRole.SOURCE,
    supersededAt: java.time.Instant? = null,
    unconfirmed: Boolean = false,
) = CertificateStanding(
    AgentIdentity(tenantId = tenantId, agentId = agentId),
    notAfter,
    revokedAt,
    agentRevokedAt,
    role,
    supersededAt,
    unconfirmed,
)

private fun withSerial(serial: String) = agent().copy(serial = serial)

@MutFlowTest
class SessionRevalidationTest {
    private val clock = MovableClock(NOW)
    private val asked = mutableListOf<Collection<String>>()

    private fun failures(
        agents: List<ConnectedAgent>,
        records: Map<String, CertificateStanding>,
    ): Map<ConnectedAgent, AgentAuthFailure> {
        val standings =
            BatchStandings { serials ->
                asked += serials
                records.filterKeys { it in serials }
            }
        return MutFlow.underTest { SessionRevalidation(standings, clock).failures(agents) }
    }

    @Test
    fun `live agents with valid certificates pass, in one lookup`() {
        val a = withSerial("a".repeat(32))
        val b = withSerial("b".repeat(32))
        assertEquals(emptyMap(), failures(listOf(a, b), mapOf(a.serial to a.standing(), b.serial to b.standing())))
        assertEquals(listOf(listOf(a.serial, b.serial)), asked.map { it.toList() })
    }

    @Test
    fun `each failure S3 knows closes only its own session`() {
        val revoked = withSerial("1".repeat(32))
        val expired = withSerial("2".repeat(32))
        val agentRevoked = withSerial("3".repeat(32))
        val forgotten = withSerial("4".repeat(32))
        val fine = withSerial("5".repeat(32))
        val records =
            mapOf(
                revoked.serial to revoked.standing(revokedAt = NOW),
                expired.serial to expired.standing(notAfter = NOW),
                agentRevoked.serial to agentRevoked.standing(agentRevokedAt = NOW),
                fine.serial to fine.standing(notAfter = NOW + Duration.ofMillis(1)),
            )
        val expected =
            mapOf(
                revoked to AgentAuthFailure.CERT_REVOKED,
                expired to AgentAuthFailure.CERT_EXPIRED,
                agentRevoked to AgentAuthFailure.AGENT_REVOKED,
                forgotten to AgentAuthFailure.CERT_UNKNOWN,
            )
        assertEquals(expected, failures(listOf(revoked, expired, agentRevoked, forgotten, fine), records))
    }

    @Test
    fun `a record that now names another agent is an identity mismatch`() {
        val agent = withSerial("6".repeat(32))
        val other = agent.copy(agentId = UUID.randomUUID()).standing()
        val found = failures(listOf(agent), mapOf(agent.serial to other))
        assertEquals(mapOf(agent to AgentAuthFailure.CERT_IDENTITY_MISMATCH), found)
    }

    @Test
    fun `a verifier's session stays open, its role is not a reason to close it`() {
        val verifier = withSerial("7".repeat(32))
        val found = failures(listOf(verifier), mapOf(verifier.serial to verifier.standing(role = AgentRole.VERIFIER)))
        assertEquals(emptyMap(), found)
    }

    @Test
    fun `no sessions, no lookup`() {
        assertEquals(emptyMap(), failures(emptyList(), emptyMap()))
        assertEquals(emptyList(), asked)
    }

    @Test
    fun `Сверка открытых потоков не закрывает поток из-за вытеснения и неподтверждённости`() {
        val superseded = withSerial("8".repeat(32))
        val unconfirmed = withSerial("9".repeat(32))
        val records =
            mapOf(
                superseded.serial to superseded.standing(supersededAt = NOW),
                unconfirmed.serial to unconfirmed.standing(unconfirmed = true),
            )
        assertEquals(emptyMap(), failures(listOf(superseded, unconfirmed), records))
    }

    @Test
    fun `Сверка открытых потоков закрывает поток при отзыве и истечении, как раньше, даже у вытесненного`() {
        val revoked = withSerial("a".repeat(32))
        val agentRevoked = withSerial("b".repeat(32))
        val expired = withSerial("c".repeat(32))
        val records =
            mapOf(
                revoked.serial to revoked.standing(supersededAt = NOW, revokedAt = NOW),
                agentRevoked.serial to agentRevoked.standing(supersededAt = NOW, agentRevokedAt = NOW),
                expired.serial to expired.standing(supersededAt = NOW, notAfter = NOW),
            )
        val expected =
            mapOf(
                revoked to AgentAuthFailure.CERT_REVOKED,
                agentRevoked to AgentAuthFailure.AGENT_REVOKED,
                expired to AgentAuthFailure.CERT_EXPIRED,
            )
        assertEquals(expected, failures(listOf(revoked, agentRevoked, expired), records))
    }
}
