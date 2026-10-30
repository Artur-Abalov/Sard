// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.extension

import org.springframework.boot.autoconfigure.AutoConfigurations
import org.springframework.boot.test.context.TestConfiguration
import org.springframework.boot.test.context.runner.ApplicationContextRunner
import org.springframework.context.annotation.Bean
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNotNull
import kotlin.test.assertTrue

private fun extension(id: String) =
    object : SardExtension {
        override val id = id
        override val displayName = id
    }

private fun edition(id: String) =
    object : EditionProvider {
        override fun edition() = Edition(id, id)

        override fun licence(): LicenceSummary? = null
    }

@TestConfiguration(proxyBeanMethods = false)
class DuplicateExtensions {
    @Bean
    fun first(): SardExtension = extension("dup")

    @Bean
    fun second(): SardExtension = extension("dup")
}

@TestConfiguration(proxyBeanMethods = false)
class TwoEditionExtensions {
    @Bean("lic-a-edition")
    fun a(): EditionProvider = edition("a")

    @Bean("lic-b-edition")
    fun b(): EditionProvider = edition("b")
}

/** Rule "Реестр расширений и заменяемые швы проверяются при старте" (spec, @startup). */
class SeamUniquenessTest {
    private val runner =
        ApplicationContextRunner()
            .withConfiguration(
                AutoConfigurations.of(
                    SardExtensionsAutoConfiguration::class.java,
                    EditionAutoConfiguration::class.java,
                ),
            )

    private fun messages(failure: Throwable?) = generateSequence(failure) { it.cause }.map { it.message }.joinToString()

    @Test
    fun `Два расширения с одним id останавливают старт`() {
        runner.withUserConfiguration(DuplicateExtensions::class.java).run { ctx ->
            assertNotNull(ctx.startupFailure)
            val text = messages(ctx.startupFailure)
            assertTrue("Duplicate Sard extension ids: [dup]" in text, text)
        }
    }

    @Test
    fun `Две реализации шва редакции и лицензии от разных расширений останавливают старт`() {
        runner.withUserConfiguration(TwoEditionExtensions::class.java).run { ctx ->
            val text = messages(ctx.startupFailure)
            assertNotNull(ctx.startupFailure)
            assertTrue("lic-a" in text && "lic-b" in text, text)
        }
    }

    @Test
    fun `Одна реализация шва стартует`() {
        runner.run { ctx -> assertEquals(null, ctx.startupFailure) }
    }
}
