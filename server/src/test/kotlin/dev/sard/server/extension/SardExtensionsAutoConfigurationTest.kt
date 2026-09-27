// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.extension

import org.springframework.boot.autoconfigure.AutoConfiguration
import org.springframework.boot.autoconfigure.AutoConfigurations
import org.springframework.boot.test.context.runner.ApplicationContextRunner
import org.springframework.context.annotation.Bean
import kotlin.test.Test
import kotlin.test.assertEquals

/** Stands in for an enterprise starter contributing an extension. */
@AutoConfiguration
class FakeEnterpriseStarter {
    @Bean
    fun ssoExtension() =
        object : SardExtension {
            override val id = "sso"
            override val displayName = "Single sign-on"
        }
}

class SardExtensionsAutoConfigurationTest {
    private val runner =
        ApplicationContextRunner()
            .withConfiguration(AutoConfigurations.of(SardExtensionsAutoConfiguration::class.java))

    @Test
    fun `without starters the registry is empty`() {
        runner.run { ctx -> assertEquals(emptyList(), ctx.getBean(ExtensionRegistry::class.java).ids()) }
    }

    @Test
    fun `a starter's extension is discovered`() {
        runner
            .withConfiguration(AutoConfigurations.of(FakeEnterpriseStarter::class.java))
            .run { ctx -> assertEquals(listOf("sso"), ctx.getBean(ExtensionRegistry::class.java).ids()) }
    }
}
