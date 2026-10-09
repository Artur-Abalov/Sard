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
