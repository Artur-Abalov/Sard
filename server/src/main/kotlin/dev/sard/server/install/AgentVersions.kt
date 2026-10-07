// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.install

private const val SEGMENTS = 3
private const val STAGE_GROUP = 4
private const val COUNTER_GROUP = 5

// The release-tag rule of scripts/release-version.sh: vX.Y.Z, vX.Y.Z-beta.N or vX.Y.Z-rc.N, N from 1,
// no leading zeros. Groups: 1-3 major, minor, patch; 4 the stage and 5 the counter ("" for a release).
private val RELEASE = Regex("""v(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-(beta|rc)\.([1-9]\d*))?""")

/** Pre-release stages in SemVer order: "beta" < "rc" as strings, and any pre-release is below the release. */
private enum class Stage {
    BETA,
    RC,
    RELEASE,
    ;

    companion object {
        /** The stage the tag names; no stage (the group is empty) is the release. */
        fun named(name: String) =
            when (name) {
                "beta" -> BETA
                "rc" -> RC
                else -> RELEASE
            }
    }
}

/**
 * A release version by SemVer 2.0 (ADR 0047). A build from git (`v1.3.2-5-gabc1234`), `dev`, a tag the
 * release rule refuses (`v0.0.1-rc1`) and a number too big for a Long are none.
 */
private data class ReleaseVersion(
    val major: Long,
    val minor: Long,
    val patch: Long,
    val stage: Stage,
    val counter: Long,
) : Comparable<ReleaseVersion> {
    override fun compareTo(other: ReleaseVersion): Int =
        compareValuesBy(
            this,
            other,
            ReleaseVersion::major,
            ReleaseVersion::minor,
            ReleaseVersion::patch,
            ReleaseVersion::stage,
            ReleaseVersion::counter,
        )

    companion object {
        fun parse(text: String?): ReleaseVersion? = text?.let(RELEASE::matchEntire)?.let(::of)

        private fun of(match: MatchResult): ReleaseVersion? {
            val groups = match.groupValues
            val core = groups.subList(1, STAGE_GROUP).mapNotNull(String::toLongOrNull)
            val counter = groups[COUNTER_GROUP].ifEmpty { "0" }.toLongOrNull()
            // A number too big for a Long is no release either: it is dropped, and the count is short.
            if (core.size != SEGMENTS || counter == null) return null
            return ReleaseVersion(core[0], core[1], core[2], Stage.named(groups[STAGE_GROUP]), counter)
        }
    }
}

/**
 * Which agents are outdated (U1b, В4; pre-releases R1, Р2): both versions are releases by the tag rule
 * and the agent's is strictly lower by SemVer 2.0. Anything else (newer, equal, unknown, a git build)
 * is not marked. [offered] is the version the server hands out, or its own when it hands out nothing:
 * its packages are always its version.
 */
class AgentVersions(
    offered: String,
) {
    private val offered = ReleaseVersion.parse(offered)

    fun outdated(agent: String?): Boolean {
        val current = ReleaseVersion.parse(agent) ?: return false
        return offered != null && current < offered
    }
}
