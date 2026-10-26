// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.persistence

import dev.sard.server.persistence.HibernateTenantBridge.Companion.SYSTEM_TENANT_ID
import org.hibernate.Session
import org.hibernate.SessionFactory
import java.sql.Connection
import java.util.UUID

/**
 * Sessions whose tenant is named by the caller instead of the [dev.sard.server.extension.TenantResolver]:
 * the work before and around a request whose tenant comes from data, such as Enroll
 * (ADR 0013, "Системный доступ"). Each call is one session and one transaction.
 */
class TenantSessions(
    private val factory: SessionFactory,
) {
    /** Reads and writes as [tenantId]: Hibernate filters by it and stamps it on inserts. */
    fun <T> inTenant(
        tenantId: UUID,
        work: (Session) -> T,
    ): T {
        require(tenantId != SYSTEM_TENANT_ID) { "the system identifier is not a tenant" }
        return open(tenantId, readOnly = false, work)
    }

    /**
     * Reads across every tenant, in a read-only transaction. For the few lookups that
     * must find a row before its tenant is known (ADR 0013, "Целевая схема"); each
     * caller is listed in ADR 0013.
     */
    fun <T> system(work: (Session) -> T): T = open(SYSTEM_TENANT_ID, readOnly = true, work)

    /**
     * Reads as [tenantId], in a read-only transaction: for the work that must be seen to write nothing, such as the
     * state metrics' look at the self-backup. A write in [work] fails in the database.
     */
    fun <T> inTenantReadOnly(
        tenantId: UUID,
        work: (Session) -> T,
    ): T {
        require(tenantId != SYSTEM_TENANT_ID) { "the system identifier is not a tenant" }
        return open(tenantId, readOnly = true, work)
    }

    /**
     * One session and one transaction. In a transaction of a tenant the database knows the tenant too (DB2, ADR 0013):
     * `sard.tenant_id` is set for the transaction only (`set_config(..., true)`, as SET LOCAL), so a connection of the
     * pool that goes to the next transaction has none. The policies of row level security that will read it come after
     * 0.1.0; nothing reads it now.
     */
    private fun <T> open(
        tenantId: UUID,
        readOnly: Boolean,
        work: (Session) -> T,
    ): T =
        factory.withOptions().tenantIdentifier(tenantId).openSession().use { session ->
            session.fromTransaction {
                if (readOnly) session.isDefaultReadOnly = true
                session.doWork { connection: Connection -> prepare(connection, tenantId, readOnly) }
                work(session)
            }
        }

    /** SET TRANSACTION READ ONLY must come before any query of the transaction, so before the tenant is set. */
    private fun prepare(
        connection: Connection,
        tenantId: UUID,
        readOnly: Boolean,
    ) {
        // mutflow:falsePositive the Boolean execute() returns is dropped by use { }; the statement runs either way
        if (readOnly) connection.createStatement().use { it.execute("SET TRANSACTION READ ONLY") }
        if (tenantId != SYSTEM_TENANT_ID) {
            connection.prepareStatement("select set_config('sard.tenant_id', ?, true)").use {
                it.setString(1, tenantId.toString())
                // mutflow:falsePositive the Boolean execute() returns is dropped; it runs either way
                it.execute()
            }
        }
    }
}
