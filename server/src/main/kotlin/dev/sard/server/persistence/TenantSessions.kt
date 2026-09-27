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
        return open(tenantId) { session -> work(session) }
    }

    /**
     * Reads across every tenant, in a read-only transaction. For the few lookups that
     * must find a row before its tenant is known (ADR 0013, "Целевая схема"); each
     * caller is listed in ADR 0013.
     */
    fun <T> system(work: (Session) -> T): T =
        open(SYSTEM_TENANT_ID) { session ->
            session.isDefaultReadOnly = true
            session.doWork { connection: Connection ->
                connection.createStatement().use { it.execute("SET TRANSACTION READ ONLY") }
            }
            work(session)
        }

    private fun <T> open(
        tenantId: UUID,
        work: (Session) -> T,
    ): T =
        factory.withOptions().tenantIdentifier(tenantId).openSession().use { session ->
            session.fromTransaction { work(session) }
        }
}
