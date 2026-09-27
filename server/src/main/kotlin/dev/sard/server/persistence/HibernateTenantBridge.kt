// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.persistence

import dev.sard.server.extension.TenantResolver
import jakarta.persistence.EntityManagerFactory
import org.hibernate.SessionFactory
import org.hibernate.cfg.MultiTenancySettings
import org.hibernate.context.spi.CurrentTenantIdentifierResolver
import org.springframework.boot.hibernate.autoconfigure.HibernatePropertiesCustomizer
import org.springframework.context.annotation.Bean
import org.springframework.context.annotation.Configuration
import java.util.UUID

/** Feeds the [TenantResolver] to Hibernate, which stamps and filters every `@TenantId` entity. */
class HibernateTenantBridge(
    private val resolver: TenantResolver,
) : CurrentTenantIdentifierResolver<UUID> {
    override fun resolveCurrentTenantIdentifier(): UUID = resolver.currentTenantId()

    override fun validateExistingCurrentSessions() = true

    /** Only [SYSTEM_TENANT_ID] switches the tenant filter off, and only [TenantSessions.system] uses it. */
    override fun isRoot(tenantId: UUID) = tenantId == SYSTEM_TENANT_ID

    companion object {
        /** The nil UUID: never a row in `tenants`, never returned by a [TenantResolver]. */
        val SYSTEM_TENANT_ID: UUID = UUID(0, 0)
    }
}

@Configuration(proxyBeanMethods = false)
class TenancyPersistenceConfiguration {
    @Bean
    fun tenantHibernateProperties(resolver: TenantResolver) =
        HibernatePropertiesCustomizer { props ->
            props[MultiTenancySettings.MULTI_TENANT_IDENTIFIER_RESOLVER] = HibernateTenantBridge(resolver)
        }

    @Bean
    fun tenantSessions(emf: EntityManagerFactory) = TenantSessions(emf.unwrap(SessionFactory::class.java))
}
