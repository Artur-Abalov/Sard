// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.sample

import dev.sard.server.extension.Edition
import dev.sard.server.extension.EditionProvider
import dev.sard.server.extension.LicenceItem
import dev.sard.server.extension.LicenceState
import dev.sard.server.extension.LicenceSummary
import java.time.Instant

private const val HOLDER = "Pilot LLC"

/** The edition "sample" and a licence whose state is the property `sard.sample.licence`; the core only shows it. */
class SampleEdition(
    private val licence: String,
) : EditionProvider {
    override fun edition() = Edition("sample", "Sample")

    override fun licence(): LicenceSummary =
        when (licence) {
            "valid" -> reported(LicenceState.VALID, "2026-12-31T00:00:00Z")
            "expired" -> reported(LicenceState.EXPIRED, "2026-10-01T00:00:00Z")
            "absent" -> LicenceSummary(LicenceState.ABSENT)
            "invalid" -> LicenceSummary(LicenceState.INVALID)
            "failing" -> error("the licence store is not reachable")
            else -> throw IllegalArgumentException("sard.sample.licence: $licence is not valid, expired, absent, invalid or failing")
        }

    private fun reported(
        state: LicenceState,
        expiresAt: String,
    ) = LicenceSummary(state, HOLDER, Instant.parse(expiresAt), listOf(LicenceItem("Agents", "1")))
}
