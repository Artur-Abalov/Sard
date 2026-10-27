// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.persistence

import dev.sard.server.TestcontainersConfiguration
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import org.hibernate.Session
import org.junit.jupiter.api.Tag
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.context.annotation.Import
import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertTrue

/** What a transaction of [TenantSessions] is, as the database and the session tell it. */
private data class Transaction(
    val readOnlyInDatabase: String,
    val tenantInDatabase: String?,
    val readOnlyInSession: Boolean,
)

private fun look(session: Session) =
    Transaction(
        session.createNativeQuery("show transaction_read_only", String::class.java).singleResult,
        session.createNativeQuery("select current_setting('sard.tenant_id', true)", String::class.java).singleResult,
        session.isDefaultReadOnly,
    )

/**
 * Mutation tests of [TenantSessions]: which kind of transaction each entry opens and which tenant the database
 * knows in it (DB2, ADR 0013). The classes that use the sessions only on their way do not mutate them.
 */
@Tag("mutflow-isolated")
@MutFlowTest(includeTargets = [TenantSessions::class])
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.NONE,
    properties =
        [
            "spring.grpc.server.port=0",
            "sard.scheduler.interval=1h",
            "sard.freshness.interval=1h",
            "sard.verification.interval=1h",
        ],
)
@Import(TestcontainersConfiguration::class)
class TenantSessionsTest(
    @Autowired private val sessions: TenantSessions,
) {
    private val tenant = UUID.fromString("00000000-0000-0000-0000-00000000000b")

    private fun inTenant(id: UUID) = sessions.inTenant(id, ::look)

    private fun inTenantReadOnly(id: UUID) = sessions.inTenantReadOnly(id, ::look)

    @Test
    fun `The transaction of a tenant writes and the database knows the tenant`() {
        val seen = MutFlow.underTest { sessions.inTenant(tenant, ::look) }

        assertEquals(Transaction("off", tenant.toString(), readOnlyInSession = false), seen)
    }

    @Test
    fun `The read-only transaction of a tenant writes nothing and the database knows the tenant`() {
        val seen = MutFlow.underTest { sessions.inTenantReadOnly(tenant, ::look) }

        assertEquals(Transaction("on", tenant.toString(), readOnlyInSession = true), seen)
    }

    @Test
    fun `The system transaction writes nothing and the database knows no tenant`() {
        val seen = MutFlow.underTest { sessions.system(::look) }

        assertEquals("on", seen.readOnlyInDatabase)
        assertTrue(seen.readOnlyInSession)
        assertTrue(seen.tenantInDatabase.isNullOrEmpty(), "the tenant of the system read: ${seen.tenantInDatabase}")
    }

    @Test
    fun `The system identifier is no tenant of either entry`() {
        val system = HibernateTenantBridge.SYSTEM_TENANT_ID

        val write = assertFailsWith<IllegalArgumentException> { MutFlow.underTest { inTenant(system) } }
        val read = assertFailsWith<IllegalArgumentException> { MutFlow.underTest { inTenantReadOnly(system) } }

        assertEquals("the system identifier is not a tenant", write.message)
        assertEquals("the system identifier is not a tenant", read.message)
    }
}
