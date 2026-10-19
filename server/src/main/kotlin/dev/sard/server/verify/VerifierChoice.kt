// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.verify

import java.util.UUID

/**
 * Why a verifier does not take a job (V4 «Выдача»); [stored] is how REST and the history name it. Every reason but
 * [SPACE_UNKNOWN] excludes the verifier; that one is a warning (owner decision 4: free space is unknown until V5).
 */
enum class Unsuitable(
    val stored: String,
) {
    /** No session now. */
    OFFLINE("offline"),

    /** An agent older than V4, or one that never registered: it reported no verifier status or machine. */
    OUTDATED("outdated"),

    /** It reports no repository with the job's restic id: it holds no key to it. */
    NO_KEY("no_key"),

    /** None of its plugins named like the job's lists the verify action. */
    NO_PLUGIN("no_plugin"),

    /** It stands on the machine of the source's agent or of the agent that made the snapshot (V-D7). */
    SAME_HOST("same_host"),

    /** It reports less free space than the job needs. */
    LOW_SPACE("low_space"),

    /** It runs a verification already: one at a time. */
    BUSY("busy"),

    /** It does not know its free space: it may still take the job. */
    SPACE_UNKNOWN("space_unknown"),
    ;

    val excludes: Boolean get() = this != SPACE_UNKNOWN

    companion object {
        fun of(stored: String): Unsuitable = entries.single { it.stored == stored }
    }
}

/** What a job asks of a verifier: the repository's restic id, the plugin, the machines it must avoid, the space. */
data class JobNeeds(
    val repositoryId: String,
    val plugin: String,
    /** Machine ids of the source's agent and of the agent that made the snapshot; never the verifier's. */
    val sourceMachines: Set<String>,
    val neededBytes: Long,
)

/** A live verifier of the tenant as its last Register and its session describe it. */
data class Candidate(
    val agentId: UUID,
    val hostname: String,
    val online: Boolean,
    /** It sent RegisterRequest.verifier: V4 or later. */
    val reported: Boolean,
    val machineId: String?,
    /** restic repository id to the repository's name on that host. */
    val repositories: Map<String, String>,
    /** Plugins that list the verify action. */
    val verifyPlugins: Set<String>,
    /** Null when the agent does not know it. */
    val freeBytes: Long?,
    val busy: Boolean,
    /** Verification steps queued to it in the last day: the least busy one is chosen. */
    val recentVerifications: Int,
)

/** A verifier that does not take the job, with every reason. */
data class Misfit(
    val agentId: UUID,
    val hostname: String,
    val reasons: Set<Unsuitable>,
)

/** Who takes a job, or why nobody does. */
sealed interface Match {
    /** [candidate] takes it and opens the repository under [repositoryName], its own name for it. */
    data class Chosen(
        val candidate: Candidate,
        val repositoryName: String,
        val warnings: Set<Unsuitable>,
    ) : Match

    /** Nobody fits; an empty list means the tenant has no live verifier at all. */
    data class NoneFits(
        val misfits: List<Misfit>,
    ) : Match
}

/** How much free space a verification needs: the snapshot's size times [factor] plus [headroomBytes] (decision 7). */
data class SpaceNeed(
    val factor: Double,
    val headroomBytes: Long,
) {
    fun of(snapshotBytes: Long): Long = (snapshotBytes * factor).toLong() + headroomBytes
}

/**
 * The rule of V4 «Выдача»: a verifier fits when it is online, of V4 or later, holds the key and a plugin that
 * verifies, stands on another machine than the source, has the space and is idle. Of those that fit, the one with
 * the fewest recent verifications takes the job, then the lowest id, so the choice is the same on every server.
 */
object VerifierChoice {
    fun of(
        needs: JobNeeds,
        candidates: List<Candidate>,
    ): Match {
        val judged = candidates.map { it to reasons(needs, it) }
        val fitting = judged.filter { (_, reasons) -> reasons.none { it.excludes } }
        val best =
            fitting.minWithOrNull(compareBy({ it.first.recentVerifications }, { it.first.agentId }))
                ?: return Match.NoneFits(judged.map { (it, reasons) -> Misfit(it.agentId, it.hostname, reasons) })
        val (candidate, warnings) = best
        return Match.Chosen(candidate, candidate.repositories.getValue(needs.repositoryId), warnings)
    }

    private fun reasons(
        needs: JobNeeds,
        candidate: Candidate,
    ): Set<Unsuitable> =
        mapOf(
            Unsuitable.OFFLINE to !candidate.online,
            Unsuitable.OUTDATED to outdated(candidate),
            Unsuitable.NO_KEY to (needs.repositoryId !in candidate.repositories),
            Unsuitable.NO_PLUGIN to (needs.plugin !in candidate.verifyPlugins),
            Unsuitable.SAME_HOST to (candidate.machineId in needs.sourceMachines),
            Unsuitable.LOW_SPACE to lowSpace(needs, candidate.freeBytes),
            Unsuitable.BUSY to candidate.busy,
            Unsuitable.SPACE_UNKNOWN to (candidate.freeBytes == null),
        ).filterValues { it }.keys

    private fun outdated(candidate: Candidate): Boolean = !candidate.reported || candidate.machineId == null

    private fun lowSpace(
        needs: JobNeeds,
        freeBytes: Long?,
    ): Boolean = freeBytes != null && freeBytes < needs.neededBytes
}
