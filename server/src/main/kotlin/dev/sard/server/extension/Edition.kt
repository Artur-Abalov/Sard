// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.extension

import java.time.Instant

/** The edition this build reports: "free" in the open core, otherwise what the enterprise extension says. */
data class Edition(
    val id: String,
    val displayName: String,
) {
    companion object {
        /** The edition of the open core (CORE1 Р13). */
        val FREE = Edition("free", "Free")
    }
}

/** How a licence stands, as the extension that owns it reports it. The core does not judge it (Р2). */
enum class LicenceState {
    VALID,
    EXPIRED,
    ABSENT,
    INVALID,
    UNAVAILABLE,
}

/** A line of the licence the console shows as it is: the core does not interpret or enforce it (Р2). */
data class LicenceItem(
    val label: String,
    val value: String,
)

/** The summary of a licence (Р14). [holder] and [expiresAt] are null when the extension did not report them. */
data class LicenceSummary(
    val state: LicenceState,
    val holder: String? = null,
    val expiresAt: Instant? = null,
    val items: List<LicenceItem> = emptyList(),
)

/**
 * The seam for edition and licence (CORE1 Р2, Р3). The open core's implementation reports the free edition and no
 * licence. An extension replaces it with `@AutoConfiguration(before = [EditionAutoConfiguration::class])`; two
 * implementations refuse the start. A licence NEVER blocks a backup, a restore or a verification: the core
 * calls this seam only to tell what it was told, and no run, dispatch, scheduler or verification code depends on it.
 */
interface EditionProvider {
    /** Read once at start; throwing refuses the start. */
    fun edition(): Edition

    /** null in the free edition. May throw: the core then reports the state UNAVAILABLE. */
    fun licence(): LicenceSummary?
}
