// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.selfagent

import javax.sql.DataSource

/** Brings the role [SELF_ROLE] to the shape migration V202610071200 gave it (F7, Р33). */
fun interface DumpRole {
    fun reconcile()
}

/** Keeps servers that start together on one database from setting the role and its channel up at once. */
fun interface SetupLock {
    fun exclusive(action: () -> Unit)
}

/** The key of the lock: no other part of the server takes advisory locks. */
private const val LOCK_KEY = 0x5A4D_5E1FL

/**
 * The statements of migration V202610071200, repeatable: create the role if missing, give it pg_read_all_data and a
 * read-only default, and take back what PUBLIC is granted by default. Creating a role takes a superuser; for any
 * other user nothing is done, and setting the password then fails as it does for a role that is not there.
 */
private const val RECONCILE =
    """
DO ${'$'}${'$'}
BEGIN
    IF NOT (SELECT rolsuper FROM pg_roles WHERE rolname = current_user) THEN
        RETURN;
    END IF;
    IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'sard_self') THEN
        CREATE ROLE sard_self LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS
            CONNECTION LIMIT 4;
    END IF;
    GRANT pg_read_all_data TO sard_self;
    ALTER ROLE sard_self SET default_transaction_read_only = on;
    EXECUTE format('REVOKE TEMPORARY ON DATABASE %I FROM PUBLIC', current_database());
    REVOKE CREATE ON SCHEMA public FROM PUBLIC;
    REVOKE EXECUTE ON FUNCTION lo_create(oid), lo_creat(integer), lo_from_bytea(oid, bytea),
        lo_import(text), lo_import(text, oid) FROM PUBLIC;
END
${'$'}${'$'}
"""

internal class PostgresDumpRole(
    private val dataSource: DataSource,
) : DumpRole {
    override fun reconcile() {
        dataSource.connection.use { connection -> connection.createStatement().use { it.execute(RECONCILE) } }
    }
}

/**
 * A session-level advisory lock, held on a connection of its own while the action runs. It takes one pool connection
 * for the whole action, so the pool needs at least two.
 */
internal class AdvisoryLock(
    private val dataSource: DataSource,
) : SetupLock {
    override fun exclusive(action: () -> Unit) {
        dataSource.connection.use { connection ->
            connection.prepareStatement("select pg_advisory_lock(?)").use {
                it.setLong(1, LOCK_KEY)
                it.execute()
            }
            try {
                action()
            } finally {
                connection.prepareStatement("select pg_advisory_unlock(?)").use {
                    it.setLong(1, LOCK_KEY)
                    it.execute()
                }
            }
        }
    }
}
