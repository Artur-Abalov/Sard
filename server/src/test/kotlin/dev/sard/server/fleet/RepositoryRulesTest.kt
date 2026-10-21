// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.fleet

import dev.sard.server.registration.RecoveryKey
import dev.sard.server.role.AgentRole
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import java.time.Instant
import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNull

private val T0 = Instant.parse("2026-10-21T12:00:00Z")
private val A = UUID.fromString("0192f7a0-0000-7000-8000-00000000000a")
private val B = UUID.fromString("a192f7a0-0000-7000-8000-00000000000b")
private val W = UUID.fromString("3f2a9c1e-0000-7000-8000-00000000000c")
private const val R1 = "1111"
private const val R2 = "2222"
private const val SELF_COMMAND = "docker compose run self-agent repo key add"

private fun holder(
    agent: UUID,
    repositoryId: String = R1,
    name: String = "base",
    key: RecoveryKey = RecoveryKey.PRESENT,
    at: Instant = T0,
    role: AgentRole = AgentRole.SOURCE,
    builtin: Boolean = false,
    hostname: String = "host-$agent".take(10),
) = RepositoryHolder(agent, hostname, role, builtin, repositoryId, name, at, key)

private fun summaries(vararg holders: RepositoryHolder) =
    MutFlow.underTest { RepositoryRules.summaries(holders.toList()) { "$SELF_COMMAND $it" } }

private fun setup(
    verifier: UUID,
    vararg holders: RepositoryHolder,
) = MutFlow.underTest { RepositoryRules.verifierSetup(holders.toList(), verifier) }

/** V3, rule "Список репозиториев показывает признак по repository_id" (КС5, КС6) and the verifier card (КС7). */
@MutFlowTest
class RepositoryRulesTest {
    @Test
    fun `items are one per repository id, ordered by it, with the holders by agent id`() {
        val got = summaries(holder(B, R2), holder(B, R1, "two"), holder(A, R1, "one"))

        assertEquals(listOf(R1, R2), got.map { it.repositoryId })
        assertEquals(listOf(A, B), got[0].holders.map { it.agentId })
        assertEquals(listOf("one", "two"), got[0].holders.map { it.repositoryName })
        assertEquals(AgentRole.SOURCE, got[0].holders[0].role)
    }

    @Test
    fun `the flag comes from the latest register with a known value`() {
        val got =
            summaries(
                holder(A, key = RecoveryKey.ABSENT, at = T0),
                holder(B, key = RecoveryKey.PRESENT, at = T0.plusSeconds(60)),
                holder(W, key = RecoveryKey.UNKNOWN, at = T0.plusSeconds(120), role = AgentRole.VERIFIER),
            ).single()

        assertEquals(RecoveryKey.PRESENT, got.recoveryKey)
        assertEquals(B, got.reportedByAgentId)
        assertEquals(T0.plusSeconds(60), got.reportedAt)
        assertEquals(listOf(A, B, W).sortedBy { it.toString() }, got.holders.map { it.agentId })
    }

    @Test
    fun `at the same time the smaller agent id decides`() {
        val got = summaries(holder(B, key = RecoveryKey.PRESENT), holder(A, key = RecoveryKey.ABSENT)).single()

        assertEquals(RecoveryKey.ABSENT, got.recoveryKey)
        assertEquals(A, got.reportedByAgentId)
    }

    @Test
    fun `a repository only unknown is unknown without a reporter`() {
        val got = summaries(holder(A, key = RecoveryKey.UNKNOWN)).single()

        assertEquals(RecoveryKey.UNKNOWN, got.recoveryKey)
        assertNull(got.reportedAt)
        assertNull(got.reportedByAgentId)
        assertNull(got.recoveryKeyCommand)
    }

    @Test
    fun `an agent holding one repository under two names is one holder with the first name`() {
        val got = summaries(holder(A, name = "zeta"), holder(A, name = "alpha")).single()

        assertEquals(listOf("alpha"), got.holders.map { it.repositoryName })
    }

    @Test
    fun `an absent key gets the command of the source with the latest register`() {
        val got =
            summaries(
                holder(A, name = "old", key = RecoveryKey.ABSENT, at = T0),
                holder(B, name = "main", key = RecoveryKey.ABSENT, at = T0.plusSeconds(1)),
            ).single()

        assertEquals("sudo sard-agent repo key add main --for recovery", got.recoveryKeyCommand)
    }

    @Test
    fun `sources at the same time are told apart by the smaller agent id`() {
        val got =
            summaries(
                holder(B, name = "second", key = RecoveryKey.ABSENT),
                holder(A, name = "first", key = RecoveryKey.ABSENT),
            )

        assertEquals("sudo sard-agent repo key add first --for recovery", got.single().recoveryKeyCommand)
    }

    @Test
    fun `a present key has no command`() {
        assertNull(summaries(holder(A, key = RecoveryKey.PRESENT)).single().recoveryKeyCommand)
    }

    @Test
    fun `a repository held only by verifiers has no command`() {
        val got = summaries(holder(W, key = RecoveryKey.ABSENT, role = AgentRole.VERIFIER)).single()

        assertEquals(RecoveryKey.ABSENT, got.recoveryKey)
        assertNull(got.recoveryKeyCommand)
    }

    @Test
    fun `a verifier later than the source does not take the command`() {
        val got =
            summaries(
                holder(A, name = "base", key = RecoveryKey.ABSENT, at = T0),
                holder(W, name = "vrf", key = RecoveryKey.ABSENT, at = T0.plusSeconds(9), role = AgentRole.VERIFIER),
            ).single()

        assertEquals("sudo sard-agent repo key add base --for recovery", got.recoveryKeyCommand)
    }

    @Test
    fun `the built-in agent gets the command of the self-backup`() {
        val got = summaries(holder(A, name = "self", key = RecoveryKey.ABSENT, builtin = true)).single()

        assertEquals("$SELF_COMMAND self", got.recoveryKeyCommand)
    }

    @Test
    fun `a name that is not safe in a command gets none, a name with a space is quoted`() {
        val spaced = summaries(holder(A, name = "off site", key = RecoveryKey.ABSENT)).single()
        val quoted = summaries(holder(A, name = "it's", key = RecoveryKey.ABSENT)).single()
        val control = summaries(holder(A, name = "a\nb", key = RecoveryKey.ABSENT)).single()

        assertEquals("sudo sard-agent repo key add 'off site' --for recovery", spaced.recoveryKeyCommand)
        assertNull(quoted.recoveryKeyCommand)
        assertNull(control.recoveryKeyCommand)
    }

    @Test
    fun `only absent repositories are counted as without a recovery key`() {
        val items =
            summaries(
                holder(A, R1, key = RecoveryKey.ABSENT),
                holder(A, R2, name = "extra", key = RecoveryKey.PRESENT),
                holder(B, "3333", name = "third", key = RecoveryKey.UNKNOWN),
            )

        assertEquals(1, MutFlow.underTest { RepositoryRules.withoutRecoveryKey(items) })
    }

    @Test
    fun `the verifier is offered what it does not hold, with the command for the source`() {
        val got = setup(W, holder(A, name = "base", hostname = "src1"))

        val item = got.single()
        assertEquals(R1, item.repositoryId)
        assertEquals(A, item.sourceAgentId)
        assertEquals("src1", item.sourceHostname)
        assertEquals("base", item.repositoryName)
        assertEquals("sudo sard-agent repo key add base --for verifier --label vrf-3f2a9c1e", item.keyAddCommand)
        assertEquals("sudo sard-agent repo add base <address> --password-from-file <file>", item.repoAddTemplate)
    }

    @Test
    fun `a repository the verifier already holds is not offered, nor one only verifiers hold`() {
        val got =
            setup(
                W,
                holder(A, R1),
                holder(W, R1, role = AgentRole.VERIFIER),
                holder(B, R2),
                holder(UUID(7, 7), R2, role = AgentRole.VERIFIER),
                holder(UUID(8, 8), "3333", role = AgentRole.VERIFIER),
            )

        assertEquals(listOf(R2), got.map { it.repositoryId })
    }

    @Test
    fun `the source of the command is the holder with the latest register, then the smaller agent id`() {
        val newer = holder(B, name = "main", at = T0.plusSeconds(60))
        val later = setup(W, holder(A, name = "base", at = T0), newer).single()
        val tied = setup(W, holder(B, name = "second"), holder(A, name = "first")).single()

        assertEquals(B, later.sourceAgentId)
        assertEquals("sudo sard-agent repo key add main --for verifier --label vrf-3f2a9c1e", later.keyAddCommand)
        assertEquals(A, tied.sourceAgentId)
    }

    @Test
    fun `a name with a space is quoted and one that cannot be quoted gets no commands`() {
        val spaced = setup(W, holder(A, name = "off site")).single()
        val quote = setup(W, holder(A, name = "it's")).single()
        val newline = setup(W, holder(A, name = "a\nb")).single()

        val keyAdd = "sudo sard-agent repo key add 'off site' --for verifier --label vrf-3f2a9c1e"
        assertEquals(keyAdd, spaced.keyAddCommand)
        val repoAdd = "sudo sard-agent repo add 'off site' <address> --password-from-file <file>"
        assertEquals(repoAdd, spaced.repoAddTemplate)
        for (item in listOf(quote, newline)) {
            assertNull(item.keyAddCommand)
            assertNull(item.repoAddTemplate)
        }
    }

    @Test
    fun `the offers are ordered by repository id`() {
        val got = setup(W, holder(A, R2, name = "two"), holder(A, R1, name = "one"))

        assertEquals(listOf(R1, R2), got.map { it.repositoryId })
    }
}
