// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.extension

import org.springframework.boot.test.context.TestConfiguration
import org.springframework.context.annotation.Bean
import java.time.Instant

/** The extension of the tests that stand in for the sample (its id and name are the ones the specification pins). */
class FixtureExtension(
    override val id: String = "sample",
    override val displayName: String = "Sard Sample Extension",
) : SardExtension

/** The licence the fixture edition reports; a test sets it and the next test starts from [reset]. */
class FixtureEdition : EditionProvider {
    @Volatile
    var licence: () -> LicenceSummary? = { null }

    override fun edition() = Edition("sample", "Sample")

    override fun licence(): LicenceSummary? = licence.invoke()

    fun reset() {
        licence = { LicenceSummary(LicenceState.VALID) }
    }
}

val PILOT_VALID =
    LicenceSummary(
        LicenceState.VALID,
        "Pilot LLC",
        Instant.parse("2026-12-31T00:00:00Z"),
        listOf(LicenceItem("Agents", "1")),
    )

/** What the sample's starter contributes to a server: an extension and a replaced edition seam. */
@TestConfiguration(proxyBeanMethods = false)
class FixtureEditionConfiguration {
    @Bean
    fun fixtureExtension(): SardExtension = FixtureExtension()

    @Bean
    fun fixtureEdition(): FixtureEdition = FixtureEdition().also { it.licence = { PILOT_VALID } }
}

/** A second extension, to see the registry order by id. */
@TestConfiguration(proxyBeanMethods = false)
class SecondExtensionConfiguration {
    @Bean
    fun aaaExtension(): SardExtension = FixtureExtension("aaa-test", "Test extension")
}
