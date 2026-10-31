// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.extension

import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import org.springframework.boot.autoconfigure.AutoConfigurations
import org.springframework.boot.test.context.runner.ApplicationContextRunner
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNotNull
import kotlin.test.assertNull
import kotlin.test.assertTrue

private val WRITE = Operation("DELETE", "/api/v1/sources/{sourceId}")

/** Rule "Реестр расширений и заменяемые швы проверяются при старте": the seams of edition and access, mutated. */
@MutFlowTest
class SeamMutationTest {
    private val editions =
        ApplicationContextRunner().withConfiguration(AutoConfigurations.of(EditionAutoConfiguration::class.java))
    private val policies =
        ApplicationContextRunner().withConfiguration(AutoConfigurations.of(AccessPolicyAutoConfiguration::class.java))

    private class Outcome(
        val failure: Throwable?,
        val edition: Edition?,
        val licence: LicenceSummary?,
        val permitted: Boolean?,
    )

    private fun startEdition(runner: ApplicationContextRunner): Outcome =
        MutFlow.underTest {
            var outcome: Outcome? = null
            runner.run { ctx ->
                val failure = ctx.startupFailure
                outcome =
                    if (failure != null) {
                        Outcome(failure, null, null, null)
                    } else {
                        Outcome(
                            null,
                            ctx.getBean(ReportedEdition::class.java).edition,
                            ctx.getBean(EditionProvider::class.java).licence(),
                            null,
                        )
                    }
            }
            checkNotNull(outcome)
        }

    private fun startPolicy(runner: ApplicationContextRunner): Outcome =
        MutFlow.underTest {
            var outcome: Outcome? = null
            runner.run { ctx ->
                val failure = ctx.startupFailure
                val policy = if (failure == null) ctx.getBean(AccessPolicy::class.java) else null
                val permitted = policy?.permits(Subject.ADMINISTRATOR, WRITE)
                outcome = Outcome(failure, null, null, permitted)
            }
            checkNotNull(outcome)
        }

    private fun text(failure: Throwable?) = generateSequence(failure) { it.cause }.map { it.message }.joinToString()

    @Test
    fun `The open core reports the free edition and no licence`() {
        val outcome = startEdition(editions)

        assertNull(outcome.failure)
        assertEquals(Edition("free", "Free"), outcome.edition)
        assertNull(outcome.licence)
    }

    @Test
    fun `An extension replaces the edition and the licence`() {
        val outcome = startEdition(editions.withConfiguration(AutoConfigurations.of(FakeEditionStarter::class.java)))

        assertEquals(Edition("pro", "Pro"), outcome.edition)
        assertEquals(LicenceState.VALID, outcome.licence?.state)
    }

    @Test
    fun `A seam that throws on the edition refuses the start and keeps the cause`() {
        val outcome = startEdition(editions.withUserConfiguration(BrokenEditionExtension::class.java))

        assertNotNull(outcome.failure)
        assertTrue("Edition unavailable: edition broken" in text(outcome.failure), text(outcome.failure))
        val refusal = generateSequence(outcome.failure) { it.cause }.filterIsInstance<IllegalStateException>().toList()
        assertTrue(refusal.any { it.message == "Edition unavailable: edition broken" }, text(outcome.failure))
    }

    @Test
    fun `Two implementations of the edition seam are named with the bean of the configuration that declares each`() {
        val outcome = startEdition(editions.withUserConfiguration(TwoEditionExtensions::class.java))

        val expected =
            "Several implementations of EditionProvider, only one may replace it: " +
                "lic-a-edition (twoEditionExtensions), " +
                "lic-b-edition (twoEditionExtensions)"
        assertTrue(expected in text(outcome.failure), text(outcome.failure))
    }

    @Test
    fun `The access policy of the core permits the administrator everything`() {
        val outcome = startPolicy(policies)

        assertNull(outcome.failure)
        assertEquals(true, outcome.permitted)
    }

    @Test
    fun `An extension replaces the access policy`() {
        val outcome = startPolicy(policies.withConfiguration(AutoConfigurations.of(FakePolicyStarter::class.java)))

        assertNull(outcome.failure)
        assertEquals(false, outcome.permitted)
    }

    @Test
    fun `Two access policies are named with the bean of the configuration that declares each`() {
        val outcome = startPolicy(policies.withUserConfiguration(TwoPolicyExtensions::class.java))

        val expected =
            "Several implementations of AccessPolicy, only one may replace it: " +
                "pol-a-policy (twoPolicyExtensions), pol-b-policy (twoPolicyExtensions)"
        assertTrue(expected in text(outcome.failure), text(outcome.failure))
        assertFalse(outcome.permitted == true)
    }
}
