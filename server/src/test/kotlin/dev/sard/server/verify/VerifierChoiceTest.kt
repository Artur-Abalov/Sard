// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.verify

import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertIs

private val REPOSITORY = "a".repeat(64)
private val OTHER_REPOSITORY = "b".repeat(64)
private val SOURCE_MACHINE = "1".repeat(64)
private val VERIFIER_MACHINE = "2".repeat(64)
private const val GIB = 1L shl 30

private fun needs(
    neededBytes: Long = 10 * GIB,
    sourceMachines: Set<String> = setOf(SOURCE_MACHINE),
) = JobNeeds(REPOSITORY, "postgresql", sourceMachines, neededBytes)

private fun verifier(
    id: Int = 1,
    online: Boolean = true,
    reported: Boolean = true,
    machineId: String? = VERIFIER_MACHINE,
    repositories: Map<String, String> = mapOf(REPOSITORY to "shared"),
    plugins: Set<String> = setOf("postgresql"),
    freeBytes: Long? = 100 * GIB,
    busy: Boolean = false,
    recent: Int = 0,
) = Candidate(
    agentId = UUID(0, id.toLong()),
    hostname = "verifier-$id",
    online = online,
    reported = reported,
    machineId = machineId,
    repositories = repositories,
    verifyPlugins = plugins,
    freeBytes = freeBytes,
    busy = busy,
    recentVerifications = recent,
)

/** V4 «Выдача»: which verifier gets a job, and why each other one does not (V-D7, ADR 0008). */
@MutFlowTest
class VerifierChoiceTest {
    private fun reasonsOf(candidate: Candidate): Set<Unsuitable> {
        val match = MutFlow.underTest { VerifierChoice.of(needs(), listOf(candidate)) }
        return assertIs<Match.NoneFits>(match).misfits.single().reasons
    }

    @Test
    fun `a verifier with key, plugin and space on another machine gets the job under its own repository name`() {
        val match = MutFlow.underTest { VerifierChoice.of(needs(), listOf(verifier())) }

        val chosen = assertIs<Match.Chosen>(match)
        assertEquals(UUID(0, 1), chosen.candidate.agentId)
        assertEquals("shared", chosen.repositoryName)
        assertEquals(emptySet(), chosen.warnings)
    }

    @Test
    fun `a verifier on the machine of the source never gets the job`() {
        assertEquals(setOf(Unsuitable.SAME_HOST), reasonsOf(verifier(machineId = SOURCE_MACHINE)))
    }

    @Test
    fun `the machine of the agent that made the snapshot counts as the source's too`() {
        val needs = needs(sourceMachines = setOf(SOURCE_MACHINE, VERIFIER_MACHINE))

        val match = MutFlow.underTest { VerifierChoice.of(needs, listOf(verifier())) }

        assertEquals(setOf(Unsuitable.SAME_HOST), assertIs<Match.NoneFits>(match).misfits.single().reasons)
    }

    @Test
    fun `a verifier without the repository's key does not fit`() {
        assertEquals(setOf(Unsuitable.NO_KEY), reasonsOf(verifier(repositories = mapOf(OTHER_REPOSITORY to "x"))))
    }

    @Test
    fun `a verifier without a plugin that verifies does not fit`() {
        assertEquals(setOf(Unsuitable.NO_PLUGIN), reasonsOf(verifier(plugins = setOf("files"))))
    }

    @Test
    fun `a verifier with less free space than needed does not fit, one with exactly enough does`() {
        assertEquals(setOf(Unsuitable.LOW_SPACE), reasonsOf(verifier(freeBytes = 10 * GIB - 1)))

        val match = MutFlow.underTest { VerifierChoice.of(needs(), listOf(verifier(freeBytes = 10 * GIB))) }

        assertIs<Match.Chosen>(match)
    }

    @Test
    fun `unknown free space does not exclude a verifier but is a warning`() {
        val match = MutFlow.underTest { VerifierChoice.of(needs(), listOf(verifier(freeBytes = null))) }

        assertEquals(setOf(Unsuitable.SPACE_UNKNOWN), assertIs<Match.Chosen>(match).warnings)
    }

    @Test
    fun `an offline, a busy and an older agent do not fit`() {
        assertEquals(setOf(Unsuitable.OFFLINE), reasonsOf(verifier(online = false)))
        assertEquals(setOf(Unsuitable.BUSY), reasonsOf(verifier(busy = true)))
        assertEquals(setOf(Unsuitable.OUTDATED), reasonsOf(verifier(reported = false)))
    }

    @Test
    fun `an agent that never reported its machine does not fit`() {
        assertEquals(setOf(Unsuitable.OUTDATED), reasonsOf(verifier(machineId = null)))
    }

    @Test
    fun `every reason of a verifier is named`() {
        val candidate = verifier(online = false, repositories = emptyMap(), plugins = emptySet(), freeBytes = 0)

        val reasons = reasonsOf(candidate)

        assertEquals(setOf(Unsuitable.OFFLINE, Unsuitable.NO_KEY, Unsuitable.NO_PLUGIN, Unsuitable.LOW_SPACE), reasons)
    }

    @Test
    fun `of several that fit, the one with the fewest recent verifications gets the job, then the lower id`() {
        val busiest = verifier(id = 1, recent = 3)
        val idle = verifier(id = 3, recent = 1)
        val alsoIdle = verifier(id = 2, recent = 1)

        val match = MutFlow.underTest { VerifierChoice.of(needs(), listOf(busiest, idle, alsoIdle)) }

        assertEquals(UUID(0, 2), assertIs<Match.Chosen>(match).candidate.agentId)
    }

    @Test
    fun `no verifier at all is a misfit list without entries`() {
        val match = MutFlow.underTest { VerifierChoice.of(needs(), emptyList()) }

        assertEquals(emptyList(), assertIs<Match.NoneFits>(match).misfits)
    }

    @Test
    fun `the space a job needs is the snapshot's size times the factor plus the headroom`() {
        val need = SpaceNeed(factor = 2.0, headroomBytes = GIB)

        assertEquals(5 * GIB, MutFlow.underTest { need.of(2 * GIB) })
    }
}
