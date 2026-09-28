// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.e2e

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

/** The agent image carries the agent of the current code and the pinned restic (ADR 0017, 0018). */
class AgentImageSmokeTest {
    @Test
    fun `the agent prints the version it was built with`() {
        assertEquals("sard-agent ${E2e.version}", AgentImage.run(AgentImage.AGENT_BINARY, "--version").trim())
    }

    @Test
    fun `the image carries restic of the version in restic-version`() {
        val output = AgentImage.run(AgentImage.RESTIC_BINARY, "version")
        assertTrue(output.startsWith("restic ${E2e.resticVersion} "), output)
    }
}
