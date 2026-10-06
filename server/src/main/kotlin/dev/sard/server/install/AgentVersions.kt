// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.install

private const val SEGMENTS = 3
private val RELEASE = Regex("""v(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)""")

/** A release version `vX.Y.Z`; a build from git (`v1.3.2-5-gabc1234`), `dev` or a pre-release is none. */
private data class ReleaseVersion(
    val major: Long,
    val minor: Long,
    val patch: Long,
) : Comparable<ReleaseVersion> {
    override fun compareTo(other: ReleaseVersion): Int =
        compareValuesBy(this, other, ReleaseVersion::major, ReleaseVersion::minor, ReleaseVersion::patch)

    companion object {
        fun parse(text: String?): ReleaseVersion? = text?.let(RELEASE::matchEntire)?.let(::of)

        private fun of(match: MatchResult): ReleaseVersion? {
            // A number too big for a Long is no release either: it is dropped, and the count is short.
            val numbers = match.groupValues.drop(1).mapNotNull(String::toLongOrNull)
            return if (numbers.size == SEGMENTS) ReleaseVersion(numbers[0], numbers[1], numbers[2]) else null
        }
    }
}

/**
 * Which agents are outdated (U1b, В4): both versions are releases and the agent's is strictly lower by
 * semver. Anything else (newer, equal, unknown, a git build) is not marked. [offered] is the version
 * the server hands out, or its own when it hands out nothing: its packages are always its version.
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
