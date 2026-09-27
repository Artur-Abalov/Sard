// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.verify

import org.springframework.stereotype.Component
import java.time.Instant

/** Orchestrates restore verification runs and remembers their outcome. */
interface RestoreVerifications {
    /** Time of the last successful restore verification, or null if none happened yet. */
    fun lastVerifiedAt(): Instant?
}

/** Placeholder until restore verification exists (roadmap: restore verification, stage 2). */
@Component
class NoRestoreVerifications : RestoreVerifications {
    override fun lastVerifiedAt(): Instant? = null
}
