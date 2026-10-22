// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.fleet

import dev.sard.server.registration.RecoveryKey
import dev.sard.server.role.AgentRole
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNull

private const val SELF_COMMAND = "docker compose run self-agent repo key add"

private fun summaries(vararg holders: RepositoryHolder) =
    MutFlow.underTest { RecoveryKeyRules.summaries(holders.toList()) { "$SELF_COMMAND $it" } }

/** V3, rule "Список репозиториев показывает признак по repository_id" (КС5, КС6). */
@MutFlowTest
class RecoveryKeyRulesTest {
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
                holder(A, key = RecoveryKey.ABSENT, at = REGISTERED_AT),
                holder(B, key = RecoveryKey.PRESENT, at = REGISTERED_AT.plusSeconds(60)),
                holder(W, key = RecoveryKey.UNKNOWN, at = REGISTERED_AT.plusSeconds(120), role = AgentRole.VERIFIER),
            ).single()

        assertEquals(RecoveryKey.PRESENT, got.recoveryKey)
        assertEquals(B, got.reportedByAgentId)
        assertEquals(REGISTERED_AT.plusSeconds(60), got.reportedAt)
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
                holder(A, name = "old", key = RecoveryKey.ABSENT, at = REGISTERED_AT),
                holder(B, name = "main", key = RecoveryKey.ABSENT, at = REGISTERED_AT.plusSeconds(1)),
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
                holder(A, name = "base", key = RecoveryKey.ABSENT, at = REGISTERED_AT),
                holder(
                    W,
                    name = "vrf",
                    key = RecoveryKey.ABSENT,
                    at = REGISTERED_AT.plusSeconds(9),
                    role = AgentRole.VERIFIER,
                ),
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

        assertEquals(1, MutFlow.underTest { RecoveryKeyRules.withoutRecoveryKey(items) })
    }
}
