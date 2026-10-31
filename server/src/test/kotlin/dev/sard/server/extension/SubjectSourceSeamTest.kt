// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.extension

import dev.sard.server.auth.CoreSubjectSource
import dev.sard.server.auth.CoreSubjectSourceAutoConfiguration
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import org.springframework.boot.autoconfigure.AutoConfiguration
import org.springframework.boot.autoconfigure.AutoConfigurations
import org.springframework.boot.test.context.TestConfiguration
import org.springframework.boot.test.context.runner.ApplicationContextRunner
import org.springframework.context.annotation.Bean
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertIs
import kotlin.test.assertNotNull
import kotlin.test.assertNull
import kotlin.test.assertTrue

private val ROBOT = Subject("svc", "robot")

@AutoConfiguration(before = [SubjectSourceAutoConfiguration::class])
class FakeSubjectStarter {
    @Bean
    fun subjects(): SubjectSource = SubjectSource { ROBOT }
}

@TestConfiguration(proxyBeanMethods = false)
class TwoSubjectExtensions {
    @Bean("subj-a-source")
    fun a(): SubjectSource = SubjectSource { null }

    @Bean("subj-b-source")
    fun b(): SubjectSource = SubjectSource { null }
}

/** Rule "Реестр расширений и заменяемые швы проверяются при старте": the seam of the subject (Р26). */
@MutFlowTest
class SubjectSourceSeamTest {
    private val runner =
        ApplicationContextRunner().withConfiguration(
            AutoConfigurations.of(
                SubjectSourceAutoConfiguration::class.java,
                CoreSubjectSourceAutoConfiguration::class.java,
            ),
        )

    private class Outcome(
        val failure: Throwable?,
        val source: SubjectSource?,
    )

    private fun start(runner: ApplicationContextRunner): Outcome =
        MutFlow.underTest {
            var outcome: Outcome? = null
            runner.run { ctx ->
                val failure = ctx.startupFailure
                outcome = Outcome(failure, if (failure == null) ctx.getBean(SubjectSource::class.java) else null)
            }
            checkNotNull(outcome)
        }

    private fun text(failure: Throwable?) = generateSequence(failure) { it.cause }.map { it.message }.joinToString()

    @Test
    fun `The open core names the subject of its own session`() {
        val outcome = start(runner)

        assertNull(outcome.failure)
        assertIs<CoreSubjectSource>(outcome.source)
    }

    @Test
    fun `An extension replaces the source of the subject`() {
        val outcome = start(runner.withConfiguration(AutoConfigurations.of(FakeSubjectStarter::class.java)))

        assertNull(outcome.failure)
        assertEquals(ROBOT, outcome.source?.current())
    }

    @Test
    fun `Two sources of the subject are named with the bean of the configuration that declares each`() {
        val outcome = start(runner.withUserConfiguration(TwoSubjectExtensions::class.java))

        assertNotNull(outcome.failure)
        val expected =
            "Several implementations of SubjectSource, only one may replace it: " +
                "subj-a-source (twoSubjectExtensions), subj-b-source (twoSubjectExtensions)"
        assertTrue(expected in text(outcome.failure), text(outcome.failure))
    }
}
