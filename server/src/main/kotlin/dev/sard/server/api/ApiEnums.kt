// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import com.fasterxml.jackson.annotation.JsonCreator
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

/** How far an agent certificate is from the end of its lifetime, by the server's clock (C1 phase 3, П33). */
@Schema(enumAsRef = true)
enum class AgentCertificateState {
    @JsonProperty("ok")
    OK,

    @JsonProperty("renewal_due")
    RENEWAL_DUE,

    @JsonProperty("expiring")
    EXPIRING,

    @JsonProperty("expired")
    EXPIRED,
}

/** What an agent may do, fixed when its token is issued (V1): a source backs up, a verifier verifies. */
@Schema(enumAsRef = true)
enum class AgentRole {
    @JsonProperty("source")
    SOURCE,

    @JsonProperty("verifier")
    VERIFIER,
    ;

    companion object {
        /**
         * Only the exact wire spelling: Jackson would take 1 for the second constant of an enum, and a role
         * is not something to guess (V1: "SOURCE", "" and 1 are errors at the field).
         */
        @JvmStatic
        @JsonCreator
        fun fromWire(text: String): AgentRole =
            when (text) {
                "source" -> SOURCE
                "verifier" -> VERIFIER
                else -> throw IllegalArgumentException("not a role")
            }
    }
}

/**
 * Whether a repository has a recovery key, as its agent reported it (V3). unknown is an agent older than V3 or one
 * that could not read the keys: it is not absent.
 */
@Schema(enumAsRef = true)
enum class RecoveryKeyFlag {
    @JsonProperty("present")
    PRESENT,

    @JsonProperty("absent")
    ABSENT,

    @JsonProperty("unknown")
    UNKNOWN,
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

/**
 * What a run came to, for a person to read; the server derives it from the run, its step and its cancel (J1a). A
 * failed run has only the status failed (ADR 0038): the outcome says timed_out, rejected or lost.
 */
@Schema(enumAsRef = true)
enum class RunOutcome {
    @JsonProperty("queued")
    QUEUED,

    @JsonProperty("dispatched")
    DISPATCHED,

    @JsonProperty("running")
    RUNNING,

    @JsonProperty("cancelling")
    CANCELLING,

    @JsonProperty("succeeded")
    SUCCEEDED,

    @JsonProperty("failed")
    FAILED,

    @JsonProperty("timed_out")
    TIMED_OUT,

    @JsonProperty("rejected")
    REJECTED,

    @JsonProperty("lost")
    LOST,

    @JsonProperty("cancelled")
    CANCELLED,

    @JsonProperty("cancel_unconfirmed")
    CANCEL_UNCONFIRMED,
}

/** How a cancel of a run ended: pending until the run is final. */
@Schema(enumAsRef = true)
enum class CancelResult {
    @JsonProperty("pending")
    PENDING,

    @JsonProperty("cancelled")
    CANCELLED,

    @JsonProperty("unconfirmed")
    UNCONFIRMED,

    @JsonProperty("finished_first")
    FINISHED_FIRST,
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

    @JsonProperty("pg_owners_not_restored")
    PG_OWNERS_NOT_RESTORED,

    @JsonProperty("pg_same_cluster")
    PG_SAME_CLUSTER,
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

/** Who closed a step as timed out (J1b). */
@Schema(enumAsRef = true)
enum class StepTimedOutBy {
    @JsonProperty("agent")
    AGENT,

    @JsonProperty("server")
    SERVER,
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
