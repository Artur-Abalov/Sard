// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.metrics

/**
 * The names of every metric of Sard, written here and nowhere else (H1 phase 3, decision 4): ArchitectureTest checks
 * that each is written once in main and that no builder is given a literal. Micrometer's dotted names reach
 * Prometheus with an underscore for each dot, `_total` after a counter and the unit after a timer, so the exposition
 * carries the `sard_` prefix.
 */
object SardMetricNames {
    // Agents and the stream.
    const val AGENTS_CONNECTED = "sard.agents.connected"
    const val AGENT_DUPLICATE_SESSIONS = "sard.agent.duplicate.sessions"
    const val AGENT_CLOCK_SKEW = "sard.agent.clock.skew"

    // Dispatch and results.
    const val RUN_STEPS_QUEUED = "sard.run.steps.queued"
    const val RUN_STEPS_DISPATCHED = "sard.run.steps.dispatched"
    const val RUN_STEPS_REDISPATCHED = "sard.run.steps.redispatched"
    const val RUN_STEPS_DISPATCH_FAILED = "sard.run.steps.dispatch.failed"
    const val RUN_STEP_RESULTS = "sard.run.step.results"
    const val RUN_STEP_LOGS_DROPPED = "sard.run.step.logs.dropped"
    const val RUN_STEP_LOGS_TRUNCATED = "sard.run.step.logs.truncated"
    const val RUN_FINISHED = "sard.run.finished"
    const val RUN_DURATION = "sard.run.duration"

    // Notifications.
    const val NOTIFY_PENDING = "sard.notify.pending"
    const val NOTIFY_SENT = "sard.notify.sent"
    const val NOTIFY_RETRIES = "sard.notify.retries"
    const val NOTIFY_UNDELIVERED = "sard.notify.undelivered"
    const val NOTIFY_DELIVERIES_UNDELIVERED = "sard.notify.deliveries.undelivered"

    // Scheduler and retention.
    const val SCHEDULER_FIRES = "sard.scheduler.fires"
    const val SCHEDULER_LAG_SECONDS = "sard.scheduler.lag.seconds"
    const val RETENTION_DELETED = "sard.retention.deleted"

    // The state of the installation, refreshed by a tick (StateRefresh).
    const val SOURCE_LAST_BACKUP_SUCCESS = "sard.source.last.backup.success.timestamp.seconds"
    const val SOURCE_LAST_VERIFICATION_SUCCESS = "sard.source.last.verification.success.timestamp.seconds"
    const val AGENTS = "sard.agents"
    const val AGENTS_ONLINE = "sard.agents.online"
    const val RUNS_ACTIVE = "sard.runs.active"
    const val VERIFICATION_JOBS = "sard.verification.jobs"
    const val AGENT_CERTIFICATE_NEXT_EXPIRY = "sard.agent.certificate.next.expiry.timestamp.seconds"
    const val CA_CERTIFICATE_EXPIRY = "sard.ca.certificate.expiry.timestamp.seconds"
    const val SELF_BACKUP_HEALTH = "sard.self.backup.health"
    const val SELF_BACKUP_LAST_SUCCESS = "sard.self.backup.last.success.timestamp.seconds"
    const val METRICS_STATE_REFRESH_TIMESTAMP = "sard.metrics.state.refresh.timestamp.seconds"
    const val METRICS_STATE_REFRESH_FAILURES = "sard.metrics.state.refresh.failures"

    // The server's database (DB1): 1 per way it differs from a new installation's, read at start.
    const val DATABASE_SETTINGS_MISMATCH = "sard.database.settings.mismatch"
}
