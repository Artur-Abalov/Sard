// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.fleet

import dev.sard.server.role.AgentRole
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNull

private fun setup(
    verifier: UUID,
    vararg holders: RepositoryHolder,
) = MutFlow.underTest { VerifierSetupRules.verifierSetup(holders.toList(), verifier) }

/** V3, the verifier card (КС7). */
@MutFlowTest
class VerifierSetupRulesTest {
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
        val newer = holder(B, name = "main", at = REGISTERED_AT.plusSeconds(60))
        val later = setup(W, holder(A, name = "base", at = REGISTERED_AT), newer).single()
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
