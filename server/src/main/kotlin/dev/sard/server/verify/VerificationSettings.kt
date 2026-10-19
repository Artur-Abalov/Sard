// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.verify

import org.springframework.boot.context.properties.ConfigurationProperties
import java.time.Duration
import java.time.ZoneId

private const val GIB = 1L shl 30
private const val SPACE_FACTOR = 2.0
private const val STEP_TIMEOUT_HOURS = 6L
private const val TICK_SECONDS = 10L

/** The settings of restore verification (V4); the defaults are the owner's decision 7, no V0 numbers exist yet. */
data class VerificationSettings(
    /** The cycle of a tenant that has none yet: every night after the backup window. */
    val defaultCron: String = "30 4 * * *",
    /** Its zone: the installation's. */
    val defaultTimezone: String = ZoneId.systemDefault().id,
    val space: SpaceNeed = SpaceNeed(SPACE_FACTOR, GIB),
    /** RunStep.timeout of a VERIFY step. */
    val stepTimeout: Duration = Duration.ofHours(STEP_TIMEOUT_HOURS),
    /** How far back the verifications counted for «the least busy verifier» go. */
    val recentWindow: Duration = Duration.ofDays(1),
)

/** `sard.verification.*`. */
@ConfigurationProperties("sard.verification")
data class VerificationProperties(
    val interval: Duration = Duration.ofSeconds(TICK_SECONDS),
    val defaultCron: String = "30 4 * * *",
    /** Empty: the installation's zone. */
    val defaultTimezone: String = "",
    val spaceFactor: Double = SPACE_FACTOR,
    val spaceHeadroom: Long = GIB,
    val stepTimeout: Duration = Duration.ofHours(STEP_TIMEOUT_HOURS),
) {
    fun settings(): VerificationSettings {
        problem()?.let { throw IllegalArgumentException("sard.verification.$it") }
        return VerificationSettings(
            defaultCron = defaultCron,
            defaultTimezone = defaultTimezone.ifEmpty { ZoneId.systemDefault().id },
            space = SpaceNeed(spaceFactor, spaceHeadroom),
            stepTimeout = stepTimeout,
        )
    }

    /** The first setting out of range, as the rest of its message; null when all are fine. */
    private fun problem(): String? =
        mapOf(
            "interval must be positive" to !interval.isPositive,
            "space-factor must be at least 1" to (spaceFactor < 1.0),
            "space-headroom must not be negative" to (spaceHeadroom < 0),
            "step-timeout must be positive" to !stepTimeout.isPositive,
        ).entries.firstOrNull { it.value }?.key
}
