// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.onboarding

import dev.sard.server.api.GuardTargets
import dev.sard.server.extension.TenantResolver
import dev.sard.server.fleet.Agents
import dev.sard.server.selfbackup.KeysConfirmed
import dev.sard.server.selfbackup.SelfBackups
import java.time.Instant
import java.util.UUID

/** What the wizard asks of the self-backup (F4b): whether its first backups are done, and the key copy. */
interface SelfBackupGate {
    /** Both system sources of the default tenant backed up successfully after the current binding. */
    fun firstBackupDone(): Boolean

    /** Throws SelfBackupNotConfigured or FirstBackupPending. */
    fun confirmKeys(
        tenantId: UUID,
        now: Instant,
    ): KeysConfirmed
}

/** The gate over the self-backup of the tenants: the default one for the steps, the session's for the confirmation. */
class SelfBackupsGate(
    private val selfBackups: SelfBackups,
) : SelfBackupGate {
    override fun firstBackupDone(): Boolean = selfBackups.get(TenantResolver.DEFAULT_TENANT_ID).firstBackupDone

    override fun confirmKeys(
        tenantId: UUID,
        now: Instant,
    ): KeysConfirmed = selfBackups.confirmKeys(tenantId, now)
}

/**
 * The objects a mutating request names, as the session's tenant knows them (D8, Р22): each is asked of the package
 * that owns it, through the tenant's session, never by SQL past the tenant filter (ADR 0013, rule 8).
 */
class TenantGuardTargets(
    private val selfBackups: SelfBackups,
    private val agents: Agents,
    private val tenants: TenantResolver,
) : GuardTargets {
    override fun isSystemSource(id: UUID): Boolean = selfBackups.isSystemSource(tenants.currentTenantId(), id)

    override fun isBuiltinAgent(id: UUID): Boolean = agents.isLiveBuiltin(tenants.currentTenantId(), id)
}
