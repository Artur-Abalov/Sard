// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.database

import dev.sard.server.TestcontainersConfiguration
import dev.sard.server.persistence.TenantSessions
import org.hibernate.Session
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.context.annotation.Import
import org.springframework.jdbc.core.JdbcTemplate
import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

private const val SETTING = "select current_setting('sard.tenant_id', true)"

/**
 * Rule "Тенант каждой транзакции виден базе" of docs/specs/server/database-roles.feature. The pool has one connection,
 * so the transaction after a tenant's runs on the very connection the tenant's did.
 */
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.NONE,
    properties =
        [
            "spring.grpc.server.port=0",
            "sard.scheduler.interval=1h",
            "sard.freshness.interval=1h",
            "sard.verification.interval=1h",
            "spring.datasource.hikari.maximum-pool-size=1",
            "spring.datasource.hikari.connection-timeout=5000",
            "spring.datasource.hikari.leak-detection-threshold=2000",
        ],
)
@Import(TestcontainersConfiguration::class)
class TenantSettingIntegrationTest(
    @Autowired private val sessions: TenantSessions,
    @Autowired private val jdbc: JdbcTemplate,
) {
    private val tenant = UUID.fromString("00000000-0000-0000-0000-00000000000a")

    private fun setting(session: Session): String? = session.createNativeQuery(SETTING, String::class.java).singleResult

    @Test
    fun `В транзакции тенанта настройка тенанта базы равна тенанту`() {
        assertEquals(tenant.toString(), sessions.inTenant(tenant, ::setting))
        assertEquals(tenant.toString(), sessions.inTenantReadOnly(tenant, ::setting))
    }

    @Test
    fun `Тенант транзакции не переходит на следующую транзакцию того же соединения`() {
        sessions.inTenant(tenant) { it.createNativeQuery("select 1", Int::class.javaObjectType).singleResult }

        val after = jdbc.queryForObject(SETTING, String::class.java)
        val system = sessions.system(::setting)

        assertTrue(after.isNullOrEmpty(), "the next transaction of the connection: '$after'")
        assertTrue(system.isNullOrEmpty(), "the system read: '$system'")
    }

    @Test
    fun `Политик RLS до выпуска 0-1-0 нет`() {
        val enabled =
            jdbc.queryForList(
                "select relname from pg_class where relnamespace = 'sard'::regnamespace and relrowsecurity",
                String::class.java,
            )

        assertEquals(emptyList(), enabled)
        assertEquals(0, jdbc.queryForObject("select count(*) from pg_policy", Int::class.java))
    }
}
