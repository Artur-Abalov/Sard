// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.extension

import org.springframework.boot.autoconfigure.AutoConfiguration
import org.springframework.boot.autoconfigure.AutoConfigurations
import org.springframework.boot.test.context.runner.ApplicationContextRunner
import org.springframework.context.annotation.Bean
import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertEquals

private val ACME = UUID.fromString("7f3c1a52-0b4e-4c1d-9a55-2d8e6f0b9c11")

/** Stands in for the enterprise tenants starter; ordered before the core default as ADR 0013 requires. */
@AutoConfiguration(before = [TenancyAutoConfiguration::class])
class FakeTenantsStarter {
    @Bean
    fun tenantResolver() = TenantResolver { ACME }
}

class TenancyAutoConfigurationTest {
    private val runner =
        ApplicationContextRunner()
            .withConfiguration(AutoConfigurations.of(TenancyAutoConfiguration::class.java))

    @Test
    fun `the open core pins every caller to the default tenant`() {
        runner.run { ctx ->
            assertEquals(TenantResolver.DEFAULT_TENANT_ID, ctx.getBean(TenantResolver::class.java).currentTenantId())
        }
    }

    @Test
    fun `an enterprise resolver replaces the default`() {
        runner
            .withConfiguration(AutoConfigurations.of(FakeTenantsStarter::class.java))
            .run { ctx -> assertEquals(ACME, ctx.getBean(TenantResolver::class.java).currentTenantId()) }
    }

    @Test
    fun `the default tenant id is stable`() {
        // Seeded by Flyway V2; changing it orphans every existing row.
        assertEquals(UUID.fromString("00000000-0000-0000-0000-000000000001"), TenantResolver.DEFAULT_TENANT_ID)
    }
}
