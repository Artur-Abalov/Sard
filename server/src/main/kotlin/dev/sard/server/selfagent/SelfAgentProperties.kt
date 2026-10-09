// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.selfagent

import org.springframework.boot.context.properties.ConfigurationProperties
import org.springframework.boot.convert.DurationStyle
import java.nio.file.Path
import java.time.Duration

/** What the built-in agent mechanism needs once it is on. */
data class SelfAgentSettings(
    val dir: Path,
    val checkInterval: Duration,
)

/**
 * `sard.self-agent.*` (docs/specs/server/self-agent.feature): [dir] is SARD_SELF_DIR, the channel shared
 * with the agent next to the server; empty switches the whole mechanism off. [checkInterval] is
 * SARD_SELF_CHECK_INTERVAL, kept as text so a bad value is refused naming the variable.
 */
@ConfigurationProperties("sard.self-agent")
data class SelfAgentProperties(
    val dir: String = "",
    val checkInterval: String = "15s",
) {
    /** Null when the mechanism is off; [IllegalArgumentException] for an interval that is no positive duration. */
    fun settings(): SelfAgentSettings? = if (dir.isEmpty()) null else SelfAgentSettings(Path.of(dir), interval())

    private fun interval(): Duration {
        val parsed = runCatching { DurationStyle.detectAndParse(checkInterval) }.getOrNull()
        require(parsed != null && parsed.isPositive) {
            "SARD_SELF_CHECK_INTERVAL must be a positive duration such as 15s, got \"$checkInterval\""
        }
        return parsed
    }
}
