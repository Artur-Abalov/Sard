// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.retention

import org.springframework.boot.context.properties.ConfigurationProperties
import java.time.Duration

private const val STEP_LOGS_DAYS = 30L
private const val RUNS_DAYS = 180L
private const val VERIFICATIONS_DAYS = 180L
private const val SCHEDULE_FIRES_DAYS = 180L
private const val DELIVERIES_DAYS = 30L
private const val BATCH_SIZE = 500

/**
 * `sard.retention.*` (H1 phase 2): how long the server keeps what it records, global and not per tenant, and how
 * the cleaning loop runs. Nothing here is a rule about what is never deleted: those rules are in [RetentionPass].
 */
@ConfigurationProperties("sard.retention")
data class RetentionProperties(
    val stepLogs: Duration = Duration.ofDays(STEP_LOGS_DAYS),
    val runs: Duration = Duration.ofDays(RUNS_DAYS),
    val verifications: Duration = Duration.ofDays(VERIFICATIONS_DAYS),
    val scheduleFires: Duration = Duration.ofDays(SCHEDULE_FIRES_DAYS),
    val notificationDeliveries: Duration = Duration.ofDays(DELIVERIES_DAYS),
    val interval: Duration = Duration.ofHours(1),
    val batchSize: Int = BATCH_SIZE,
) {
    /**
     * The settings, or an [IllegalArgumentException] naming the property that stops the start. A delivery older
     * than [notifyTtl] but younger than its own term must stay: the notification planner would make it again.
     */
    fun settings(notifyTtl: Duration): RetentionSettings {
        positive("sard.retention.step-logs", stepLogs)
        positive("sard.retention.runs", runs)
        positive("sard.retention.verifications", verifications)
        positive("sard.retention.schedule-fires", scheduleFires)
        positive("sard.retention.notification-deliveries", notificationDeliveries)
        positive("sard.retention.interval", interval)
        require(batchSize >= 1) { "sard.retention.batch-size must be at least 1" }
        require(notificationDeliveries > notifyTtl) {
            "sard.retention.notification-deliveries ($notificationDeliveries) must be longer than " +
                "sard.notify.ttl ($notifyTtl)"
        }
        return RetentionSettings(
            stepLogs,
            runs,
            verifications,
            scheduleFires,
            notificationDeliveries,
            interval,
            batchSize,
        )
    }

    private fun positive(
        name: String,
        value: Duration,
    ) = require(value.isPositive) { "$name must be positive" }
}

/** [RetentionProperties] after the checks of the start. */
data class RetentionSettings(
    val stepLogs: Duration,
    val runs: Duration,
    val verifications: Duration,
    val scheduleFires: Duration,
    val notificationDeliveries: Duration,
    val interval: Duration,
    val batchSize: Int,
)
