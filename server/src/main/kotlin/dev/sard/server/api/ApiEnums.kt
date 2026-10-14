// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import com.fasterxml.jackson.annotation.JsonProperty
import io.swagger.v3.oas.annotations.media.Schema

// Wire values are lower_snake_case, the same as the CHECK constraints of the
// schema (ADR 0013, rule 7). Values that mirror the agent protocol are checked
// against the proto enums by ApiContractIntegrationTest.

/** Agent connectivity, derived from its heartbeats. */
@Schema(enumAsRef = true)
enum class AgentStatus {
    @JsonProperty("online")
    ONLINE,

    @JsonProperty("offline")
    OFFLINE,
}

/** Enrollment token lifecycle (S2b). */
@Schema(enumAsRef = true)
enum class EnrollmentTokenStatus {
    @JsonProperty("active")
    ACTIVE,

    @JsonProperty("used")
    USED,

    @JsonProperty("expired")
    EXPIRED,

    @JsonProperty("revoked")
    REVOKED,
}

/** Run state; queued, dispatched and running are active (D6: one active run per source). */
@Schema(enumAsRef = true)
enum class RunStatus {
    @JsonProperty("queued")
    QUEUED,

    @JsonProperty("dispatched")
    DISPATCHED,

    @JsonProperty("running")
    RUNNING,

    @JsonProperty("succeeded")
    SUCCEEDED,

    @JsonProperty("failed")
    FAILED,

    @JsonProperty("cancelled")
    CANCELLED,
}

/** What started a run (ADR 0013, runs.trigger); catch_up is the one run after a server downtime (D16). */
@Schema(enumAsRef = true)
enum class RunTrigger {
    @JsonProperty("schedule")
    SCHEDULE,

    @JsonProperty("manual")
    MANUAL,

    @JsonProperty("verification")
    VERIFICATION,

    @JsonProperty("catch_up")
    CATCH_UP,
}

/** What a run is (R2): a backup of a source or a restore of one of its snapshots. */
@Schema(enumAsRef = true)
enum class RunKind {
    @JsonProperty("backup")
    BACKUP,

    @JsonProperty("restore")
    RESTORE,
}

/** Why the console does not restore a snapshot. */
@Schema(enumAsRef = true)
enum class RestoreBlockedReason {
    @JsonProperty("partial")
    PARTIAL,

    @JsonProperty("forgotten")
    FORGOTTEN,

    @JsonProperty("plugin_not_supported")
    PLUGIN_NOT_SUPPORTED,
}

/** Why an agent with the snapshot's repository cannot restore it. */
@Schema(enumAsRef = true)
enum class IneligibleReason {
    @JsonProperty("agent_outdated")
    AGENT_OUTDATED,

    @JsonProperty("plugin_missing")
    PLUGIN_MISSING,
}

/** What a restore preview warns about. */
@Schema(enumAsRef = true)
enum class RestoreWarningCode {
    @JsonProperty("agent_offline")
    AGENT_OFFLINE,

    @JsonProperty("owners_not_restored")
    OWNERS_NOT_RESTORED,

    @JsonProperty("globals_change_cluster")
    GLOBALS_CHANGE_CLUSTER,
}

/** How a restore step ended, for the page to word; the server decides it from the status and the output. */
@Schema(enumAsRef = true)
enum class RestoreOutcome {
    @JsonProperty("restored")
    RESTORED,

    @JsonProperty("nothing")
    NOTHING,

    @JsonProperty("rollback_failed")
    ROLLBACK_FAILED,

    @JsonProperty("late")
    LATE,
}

/** Step state (ADR 0013, run_steps.status): the server's own states around the agent's final ones. */
@Schema(enumAsRef = true)
enum class StepStatus {
    @JsonProperty("queued")
    QUEUED,

    @JsonProperty("dispatched")
    DISPATCHED,

    @JsonProperty("running")
    RUNNING,

    @JsonProperty("succeeded")
    SUCCEEDED,

    @JsonProperty("failed")
    FAILED,

    @JsonProperty("cancelled")
    CANCELLED,

    @JsonProperty("timed_out")
    TIMED_OUT,

    @JsonProperty("rejected")
    REJECTED,

    @JsonProperty("lost")
    LOST,
}

/** Progress of a running step (proto StepPhase). */
@Schema(enumAsRef = true)
enum class StepPhase {
    @JsonProperty("accepted")
    ACCEPTED,

    @JsonProperty("preparing")
    PREPARING,

    @JsonProperty("dumping")
    DUMPING,

    @JsonProperty("uploading")
    UPLOADING,

    @JsonProperty("restoring")
    RESTORING,

    @JsonProperty("verifying")
    VERIFYING,
}

/** What a step does with a plugin (proto Action). */
@Schema(enumAsRef = true)
enum class StepAction {
    @JsonProperty("backup")
    BACKUP,

    @JsonProperty("restore")
    RESTORE,

    @JsonProperty("verify")
    VERIFY,

    @JsonProperty("run")
    RUN,
}

/** Level of a step log line (proto LogLevel). */
@Schema(enumAsRef = true)
enum class LogLevel {
    @JsonProperty("debug")
    DEBUG,

    @JsonProperty("info")
    INFO,

    @JsonProperty("warn")
    WARN,

    @JsonProperty("error")
    ERROR,
}

/** A source the server keeps itself (F6): the self-backup's database and its keys with the configuration. */
@Schema(enumAsRef = true)
enum class SystemSourceRole {
    @JsonProperty("self_database")
    SELF_DATABASE,

    @JsonProperty("self_keys")
    SELF_KEYS,
}

/** How the self-backup is doing, its worst warning (F6). */
@Schema(enumAsRef = true)
enum class SelfBackupStatus {
    @JsonProperty("not_configured")
    NOT_CONFIGURED,

    @JsonProperty("ok")
    OK,

    @JsonProperty("agent_replaced")
    AGENT_REPLACED,

    @JsonProperty("overdue")
    OVERDUE,

    @JsonProperty("failing")
    FAILING,

    @JsonProperty("schedule_disabled")
    SCHEDULE_DISABLED,
}

/** What the console warns about on every page (F6), worst first. */
@Schema(enumAsRef = true)
enum class SelfBackupWarningCode {
    @JsonProperty("agent_replaced")
    AGENT_REPLACED,

    @JsonProperty("overdue")
    OVERDUE,

    @JsonProperty("failing")
    FAILING,

    @JsonProperty("schedule_disabled")
    SCHEDULE_DISABLED,

    @JsonProperty("local_storage")
    LOCAL_STORAGE,

    @JsonProperty("keys_unconfirmed")
    KEYS_UNCONFIRMED,
}
