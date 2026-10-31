// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.extension

import java.time.Instant
import java.util.UUID

/**
 * What the core tells an extension happened (CORE1 Р5, Р18-Р25). The contract is Spring's application events: an
 * extension listens with an ordinary listener bean (`@EventListener` or `ApplicationListener<PayloadApplicationEvent>`)
 * for a subtype of this interface. An event is delivered synchronously, once, after the change is committed; a
 * listener that throws changes nothing and does not stop the other listeners. A listener must be quick.
 *
 * [type] is part of the documented contract (Р25). [actor] is the subject of the session behind the change, null for
 * the work of the server itself (the scheduler, the verification cycle, the first-start wizard).
 */
sealed interface SardEvent {
    val type: String
    val occurredAt: Instant
    val tenantId: UUID
    val actor: Subject?
}

/** An event about a run: backup, restore or verification. No attempt number and no job id (Р18). */
sealed interface RunEvent : SardEvent {
    val runId: UUID

    /** backup, restore or verification. */
    val kind: String

    /** manual, schedule, catch_up or verification: the trigger of the run as REST shows it. */
    val trigger: String
    val sourceId: UUID
}

/** `run.created`: the run exists, in status queued (Р19). */
data class RunCreated(
    override val occurredAt: Instant,
    override val tenantId: UUID,
    override val actor: Subject?,
    override val runId: UUID,
    override val kind: String,
    override val trigger: String,
    override val sourceId: UUID,
) : RunEvent {
    override val type = TYPE

    companion object {
        const val TYPE = "run.created"
    }
}

/** `run.started`: the agent accepted the first step; at most once per run (Р19). */
data class RunStarted(
    override val occurredAt: Instant,
    override val tenantId: UUID,
    override val actor: Subject?,
    override val runId: UUID,
    override val kind: String,
    override val trigger: String,
    override val sourceId: UUID,
) : RunEvent {
    override val type = TYPE

    companion object {
        const val TYPE = "run.started"
    }
}

/** `run.status_changed`: the field status of the run changed; [terminal] is true exactly once per run (Р19). */
data class RunStatusChanged(
    override val occurredAt: Instant,
    override val tenantId: UUID,
    override val actor: Subject?,
    override val runId: UUID,
    override val kind: String,
    override val trigger: String,
    override val sourceId: UUID,
    val from: String,
    val to: String,
    val terminal: Boolean,
) : RunEvent {
    override val type = TYPE

    companion object {
        const val TYPE = "run.status_changed"
    }
}

/** An event about signing in or out; [clientAddress] is the address of the request. */
sealed interface SessionEvent : SardEvent {
    val clientAddress: String
}

/** `signin.succeeded`. */
data class SignInSucceeded(
    override val occurredAt: Instant,
    override val tenantId: UUID,
    override val actor: Subject?,
    override val clientAddress: String,
) : SessionEvent {
    override val type = TYPE

    companion object {
        const val TYPE = "signin.succeeded"
    }
}

/** `signin.failed`: a wrong password. The password is in no field. */
data class SignInFailed(
    override val occurredAt: Instant,
    override val tenantId: UUID,
    override val actor: Subject?,
    override val clientAddress: String,
) : SessionEvent {
    override val type = TYPE

    companion object {
        const val TYPE = "signin.failed"
    }
}

/** `signin.locked`: the failure that locked the address, once per lock. */
data class SignInLocked(
    override val occurredAt: Instant,
    override val tenantId: UUID,
    override val actor: Subject?,
    override val clientAddress: String,
    val lockedUntil: Instant,
) : SessionEvent {
    override val type = TYPE

    companion object {
        const val TYPE = "signin.locked"
    }
}

/** `signout`: DELETE /api/v1/session. */
data class SignedOut(
    override val occurredAt: Instant,
    override val tenantId: UUID,
    override val actor: Subject?,
    override val clientAddress: String,
) : SessionEvent {
    override val type = TYPE

    companion object {
        const val TYPE = "signout"
    }
}

/** `session.expired`: [reason] is idle or absolute; [clientAddress] is that of the last request with the session. */
data class SessionExpired(
    override val occurredAt: Instant,
    override val tenantId: UUID,
    override val actor: Subject?,
    override val clientAddress: String,
    val reason: String,
) : SessionEvent {
    override val type = TYPE

    companion object {
        const val TYPE = "session.expired"
    }
}

/**
 * `settings.changed`: a request changed the stored state (Р20). [setting] is one of password, run_defaults,
 * source_created, source_updated, source_deleted, source_schedule, source_verification, verification_schedule,
 * self_backup_repository, token_created, token_revoked, agent_revoked, admin_password (the command admin-reset ran
 * while the server was stopped: published once at the next start, with no actor, Р32); [objectId] is null for the
 * settings of the installation. Never carries a secret.
 */
data class SettingsChanged(
    override val occurredAt: Instant,
    override val tenantId: UUID,
    override val actor: Subject?,
    val setting: String,
    val objectId: UUID?,
) : SardEvent {
    override val type = TYPE

    companion object {
        const val TYPE = "settings.changed"
    }
}
