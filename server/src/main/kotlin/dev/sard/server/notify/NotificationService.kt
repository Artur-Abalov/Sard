// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.notify

import org.slf4j.LoggerFactory
import java.time.Clock
import java.time.Duration
import java.time.Instant

private val log = LoggerFactory.getLogger(NotificationService::class.java)

/** Counters of the notification queue (answer В11: tagged by channel). */
interface NotifyMetrics {
    fun sent(channel: String)

    fun retried(channel: String)

    /** [reason] is the final status: failed or expired. */
    fun undelivered(
        channel: String,
        reason: String,
    )

    fun pending(count: Long)

    companion object {
        val NONE =
            object : NotifyMetrics {
                override fun sent(channel: String) = Unit

                override fun retried(channel: String) = Unit

                override fun undelivered(
                    channel: String,
                    reason: String,
                ) = Unit

                override fun pending(count: Long) = Unit
            }
    }
}

/** How much one tick takes on, and how long a claimed delivery is held (S9a). */
data class QueueSettings(
    val batch: Int,
    val lease: Duration,
    val ttl: Duration,
)

/**
 * Turns finished runs into notifications, asynchronously (OQ-047, answer В4): each [tick] plans
 * a delivery per channel for every run finished within the time to live that has none, then
 * sends the deliveries that are due. Nothing here runs in the transaction that finished a run.
 * Delivery is at least once: a crash after the send and before [Deliveries.record] sends again
 * once the lease ends.
 */
class NotificationService(
    private val deliveries: Deliveries,
    private val episodes: OverdueDeliveries,
    private val certificates: CertificateDeliveries,
    private val verifications: VerificationDeliveries,
    channels: List<NotificationChannel>,
    private val formatter: NotificationFormatter,
    private val alertFormatter: SkipAlertFormatter,
    private val overdueFormatter: OverdueFormatter,
    private val certificateFormatter: CertificateFormatter,
    private val verificationFormatter: VerificationFormatter,
    private val policy: RetryPolicy,
    private val settings: QueueSettings,
    private val clock: Clock,
    private val metrics: NotifyMetrics = NotifyMetrics.NONE,
) {
    private val channels = channels.associateBy { it.name }

    fun tick() {
        plan()
        send()
        metrics.pending(deliveries.pending())
    }

    private fun plan() {
        val now = clock.instant()
        // One cut-off for every subject: older ones expire instead of being planned.
        val since = now - settings.ttl
        for (channel in channels.keys) {
            planOperations(channel, since, now)
            planVerifications(channel, since, now)
        }
    }

    private fun planOperations(
        channel: String,
        since: Instant,
        now: Instant,
    ) {
        val runs = deliveries.unplanned(channel, since, settings.batch)
        for ((tenantId, tenantRuns) in runs.groupBy({ it.tenantId }, { it.runId })) {
            deliveries.plan(tenantId, tenantRuns, channel, now)
        }
        val alerts = deliveries.unplannedAlerts(channel, since, settings.batch)
        for ((tenantId, fires) in alerts.groupBy({ it.tenantId }, { it.fireId })) {
            deliveries.planAlerts(tenantId, fires, channel, now)
        }
        val overdue = episodes.unplanned(channel, since, settings.batch)
        for ((tenantId, tenantEpisodes) in overdue.groupBy { it.tenantId }) {
            episodes.plan(tenantId, tenantEpisodes, channel, now)
        }
        val expiring = certificates.unplanned(channel, since, settings.batch)
        for ((tenantId, tenantEvents) in expiring.groupBy({ it.tenantId }, { it.eventId })) {
            certificates.plan(tenantId, tenantEvents, channel, now)
        }
    }

    private fun planVerifications(
        channel: String,
        since: Instant,
        now: Instant,
    ) {
        val checked = verifications.unplanned(channel, since, settings.batch)
        for ((tenantId, tenantSubjects) in checked.groupBy { it.tenantId }) {
            verifications.plan(tenantId, tenantSubjects, channel, now)
        }
    }

    private fun send() {
        val now = clock.instant()
        if (channels.isEmpty()) return
        for (delivery in deliveries.due(now, channels.keys, settings.batch)) {
            val channel = channels.getValue(delivery.channel)
            runCatching { attempt(delivery, channel) }.onFailure {
                log.warn("Notification {} of {} failed; it is tried again later", delivery.id, delivery.subject, it)
            }
        }
    }

    private fun attempt(
        delivery: DueDelivery,
        channel: NotificationChannel,
    ) {
        val now = clock.instant()
        if (policy.expired(delivery.createdAt, now)) {
            finish(delivery, policy.expiry(delivery.attempts))
        } else {
            deliveries.claim(delivery, now, now + settings.lease)?.let { finish(delivery, send(delivery, it, channel)) }
        }
    }

    /** A formatter that throws leaves the delivery claimed: it is tried again when the lease ends. */
    private fun send(
        delivery: DueDelivery,
        claimed: Claimed,
        channel: NotificationChannel,
    ): Decision {
        val message = messageOf(claimed) ?: return Decision.Skipped
        return policy.decide(channel.send(message), delivery.attempts, delivery.createdAt, clock.instant())
    }

    private fun messageOf(claimed: Claimed): Message? =
        when (claimed) {
            is Claimed.Finished -> formatter.format(claimed.notice)
            is Claimed.Alert -> alertFormatter.format(claimed.notice)
            is Claimed.Overdue -> overdueFormatter.format(claimed.notice)
            is Claimed.Certificate -> claimed.notice?.let(certificateFormatter::format)
            is Claimed.Verifying -> verificationFormatter.message(claimed)
        }

    private fun finish(
        delivery: DueDelivery,
        decision: Decision,
    ) {
        if (deliveries.record(delivery, decision, clock.instant())) count(delivery, decision)
    }

    private fun count(
        delivery: DueDelivery,
        decision: Decision,
    ) {
        when (decision) {
            Decision.Delivered -> metrics.sent(delivery.channel)
            Decision.Skipped -> Unit
            is Decision.Retry -> metrics.retried(delivery.channel)
            is Decision.Failed -> undelivered(delivery, "failed", decision.reason)
            is Decision.Expired -> undelivered(delivery, "expired", decision.reason)
        }
    }

    private fun undelivered(
        delivery: DueDelivery,
        status: String,
        reason: String,
    ) {
        metrics.undelivered(delivery.channel, status)
        log.warn("Notification of {} through {} {}: {}", delivery.subject, delivery.channel, status, reason)
    }
}

private fun VerificationFormatter.message(claimed: Claimed.Verifying): Message? =
    when (claimed) {
        is Claimed.Verification -> verification(claimed.notice)
        is Claimed.Stale -> stale(claimed.notice)
        is Claimed.NoVerifier -> noVerifier(claimed.notice)
    }
