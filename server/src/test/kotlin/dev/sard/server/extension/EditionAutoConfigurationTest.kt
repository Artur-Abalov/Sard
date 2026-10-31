// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.extension

import org.springframework.boot.autoconfigure.AutoConfiguration
import org.springframework.boot.autoconfigure.AutoConfigurations
import org.springframework.boot.test.context.runner.ApplicationContextRunner
import org.springframework.context.annotation.Bean
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNull

/** Stands in for an enterprise starter replacing the seam; ordered before the core default. */
@AutoConfiguration(before = [EditionAutoConfiguration::class])
class FakeEditionStarter {
    @Bean
    fun editionProvider(): EditionProvider =
        object : EditionProvider {
            override fun edition() = Edition("pro", "Pro")

            override fun licence() = LicenceSummary(LicenceState.VALID, holder = "Pilot LLC")
        }
}

class EditionAutoConfigurationTest {
    private val runner =
        ApplicationContextRunner()
            .withConfiguration(AutoConfigurations.of(EditionAutoConfiguration::class.java))

    @Test
    fun `Статус ядра сообщает редакцию free`() {
        runner.run { ctx ->
            val provider = ctx.getBean(EditionProvider::class.java)
            assertEquals(Edition("free", "Free"), provider.edition())
            assertNull(provider.licence())
        }
    }

    @Test
    fun `Расширение заменяет шов редакции и лицензии`() {
        runner
            .withConfiguration(AutoConfigurations.of(FakeEditionStarter::class.java))
            .run { ctx ->
                val provider = ctx.getBean(EditionProvider::class.java)
                assertEquals(Edition("pro", "Pro"), provider.edition())
                assertEquals(LicenceState.VALID, provider.licence()?.state)
            }
    }

    @Test
    fun `Редакция читается у шва один раз при старте`() {
        var reads = 0
        val counting =
            object : EditionProvider {
                override fun edition(): Edition {
                    reads++
                    return Edition("pro", "Pro")
                }

                override fun licence(): LicenceSummary? = null
            }
        runner.withBean(EditionProvider::class.java, { counting }).run { ctx ->
            assertEquals(1, reads)
            assertEquals(Edition("pro", "Pro"), ctx.getBean(ReportedEdition::class.java).edition)
            assertEquals(1, reads)
        }
    }
}
