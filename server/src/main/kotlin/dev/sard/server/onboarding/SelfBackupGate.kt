// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.onboarding

import dev.sard.server.api.GuardTargets
import dev.sard.server.extension.TenantResolver
import dev.sard.server.selfbackup.KeysConfirmed
import dev.sard.server.selfbackup.SelfBackups
import org.springframework.jdbc.core.JdbcTemplate
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

/** The objects a mutating request names, as the database knows them (D8, Р22). */
class JdbcGuardTargets(
    private val jdbc: JdbcTemplate,
) : GuardTargets {
    override fun isSystemSource(id: UUID): Boolean =
        exists("select 1 from sources where id = ? and system_role is not null and deleted_at is null", id)

    override fun isBuiltinAgent(id: UUID): Boolean =
        exists("select 1 from agents where id = ? and builtin = true and revoked_at is null", id)

    private fun exists(
        sql: String,
        id: UUID,
    ): Boolean = jdbc.queryForList(sql, id).isNotEmpty()
}
