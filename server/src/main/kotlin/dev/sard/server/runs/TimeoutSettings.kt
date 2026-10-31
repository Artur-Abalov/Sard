// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.runs

import dev.sard.server.events.SettingsEvents
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

// The row of a tenant starts as the initial defaults, so the write is a plain update: it takes the row lock, a
// concurrent save is re-checked against the committed row, and the count of rows tells whether this save changed the
// stored value (CORE1 Р28).
private const val SEED = """
    insert into run_defaults (tenant_id, backup_timeout_minutes, restore_timeout_minutes, updated_at)
    values (:tenant, $BACKUP_DEFAULT_MINUTES, $RESTORE_DEFAULT_MINUTES, :now)
    on conflict (tenant_id) do nothing"""
private const val WRITE = """
    update run_defaults set backup_timeout_minutes = :backup, restore_timeout_minutes = :restore, updated_at = :now
    where tenant_id = :tenant
        and (backup_timeout_minutes, restore_timeout_minutes) is distinct from (:backup, :restore)"""

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
    private val events: SettingsEvents,
) {
    fun get(tenantId: UUID): TimeoutDefaults = sessions.inTenant(tenantId) { read(it, tenantId) }

    /** Saves [defaults]; the listeners hear of it, once committed, when it differed from what the tenant had. */
    fun set(
        tenantId: UUID,
        defaults: TimeoutDefaults,
    ): TimeoutDefaults {
        sessions.inTenant(tenantId) { session ->
            val now = clock.instant()
            session
                .createNativeMutationQuery(SEED.trimIndent())
                .setParameter("tenant", tenantId)
                .setParameter("now", now)
                .executeUpdate()
            val changed =
                session
                    .createNativeMutationQuery(WRITE.trimIndent())
                    .setParameter("tenant", tenantId)
                    .setParameter("backup", defaults.backupMinutes)
                    .setParameter("restore", defaults.restoreMinutes)
                    .setParameter("now", now)
                    .executeUpdate()
            events.changedRows(changed, session, tenantId, "run_defaults")
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
