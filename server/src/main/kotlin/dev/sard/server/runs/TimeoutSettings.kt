// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.runs

import dev.sard.server.persistence.TenantSessions
import org.hibernate.Session
import java.time.Clock
import java.util.UUID

/** What a source without its own timeout gets (ВJ1): 6 h for a backup, 12 h for a restore. */
private const val BACKUP_DEFAULT_MINUTES = 360
private const val RESTORE_DEFAULT_MINUTES = 720

// Native SQL names tenant_id explicitly (ADR 0013, rule 8).
private const val READ =
    "select backup_timeout_minutes, restore_timeout_minutes from run_defaults where tenant_id = :tenant"
private const val WRITE = """
    insert into run_defaults (tenant_id, backup_timeout_minutes, restore_timeout_minutes, updated_at)
    values (:tenant, :backup, :restore, :now)
    on conflict (tenant_id) do update set backup_timeout_minutes = excluded.backup_timeout_minutes,
        restore_timeout_minutes = excluded.restore_timeout_minutes, updated_at = excluded.updated_at"""

/** The default step timeouts of a tenant in minutes, for the sources that name none. */
data class TimeoutDefaults(
    val backupMinutes: Int,
    val restoreMinutes: Int,
) {
    companion object {
        val INITIAL = TimeoutDefaults(BACKUP_DEFAULT_MINUTES, RESTORE_DEFAULT_MINUTES)
    }
}

/**
 * The installation settings of step timeouts (ДJ8, J1b): kept per tenant in the database, [TimeoutDefaults.INITIAL]
 * until a tenant saves its own. A run takes the value when it is created (Р-J6): saving changes no run in flight.
 */
class TimeoutSettings(
    private val sessions: TenantSessions,
    private val clock: Clock,
) {
    fun get(tenantId: UUID): TimeoutDefaults = sessions.inTenant(tenantId) { read(it, tenantId) }

    fun set(
        tenantId: UUID,
        defaults: TimeoutDefaults,
    ): TimeoutDefaults {
        sessions.inTenant(tenantId) { session ->
            session
                .createNativeMutationQuery(WRITE.trimIndent())
                .setParameter("tenant", tenantId)
                .setParameter("backup", defaults.backupMinutes)
                .setParameter("restore", defaults.restoreMinutes)
                .setParameter("now", clock.instant())
                .executeUpdate()
        }
        return defaults
    }

    companion object {
        /** The defaults of [tenantId] inside the caller's transaction, for a run being created. */
        internal fun read(
            session: Session,
            tenantId: UUID,
        ): TimeoutDefaults {
            val row =
                session
                    .createNativeQuery(READ, Array<Any?>::class.java)
                    .setParameter("tenant", tenantId)
                    .list()
                    .firstOrNull() ?: return TimeoutDefaults.INITIAL
            return TimeoutDefaults((row[0] as Number).toInt(), (row[1] as Number).toInt())
        }
    }
}
