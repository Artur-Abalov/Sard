// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.selfagent

import dev.sard.server.enrollment.EnrollmentTokens
import dev.sard.server.persistence.TenantSessions
import org.junit.jupiter.api.io.TempDir
import org.mockito.Mockito.mock
import org.springframework.boot.test.context.runner.ApplicationContextRunner
import java.nio.file.Path
import javax.sql.DataSource
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNotNull
import kotlin.test.assertTrue

/**
 * Rules "Без канала механизм встроенного агента выключен", "Заданный, но непригодный канал не даёт серверу
 * стартовать" and "Недопустимый интервал проверки не даёт серверу стартовать" (@startup) of
 * docs/specs/server/self-agent.feature: the real configuration, without a database.
 */
class SelfAgentStartupTest {
    @TempDir
    lateinit var dir: Path

    private fun runner(vararg properties: String) =
        ApplicationContextRunner()
            .withUserConfiguration(SelfAgentConfiguration::class.java)
            .withBean(EnrollmentTokens::class.java, { mock(EnrollmentTokens::class.java) })
            .withBean(TenantSessions::class.java, { mock(TenantSessions::class.java) })
            .withBean(DataSource::class.java, { mock(DataSource::class.java) })
            .withPropertyValues(*properties)

    private fun failureMessage(vararg properties: String): String {
        var message = ""
        runner(*properties).run { context ->
            val failure = assertNotNull(context.startupFailure)
            message = generateSequence<Throwable>(failure) { it.cause }.mapNotNull { it.message }.joinToString("\n")
        }
        return message
    }

    @Test
    fun `Без SARD_SELF_DIR механизма нет, и недопустимый интервал ему не мешает`() {
        for (properties in listOf(emptyArray(), arrayOf("sard.self-agent.dir="))) {
            runner(*properties, "sard.self-agent.check-interval=abc").run { context ->
                assertEquals(null, context.startupFailure)
                assertEquals(0, context.getBeansOfType(SelfAgentLoop::class.java).size)
                assertEquals(0, context.getBeansOfType(SelfChannel::class.java).size)
            }
        }
    }

    @Test
    fun `Канал, которого нет, не даёт серверу стартовать и назван в сообщении`() {
        val missing = dir.resolve("missing")

        val message = failureMessage("sard.self-agent.dir=$missing")

        assertTrue("$missing" in message && "docs/operations/self-agent.md" in message, message)
    }

    @Test
    fun `Недопустимый интервал не даёт серверу стартовать и назван в сообщении`() {
        for (value in listOf("0s", "-5s", "abc")) {
            val message = failureMessage("sard.self-agent.dir=$dir", "sard.self-agent.check-interval=$value")

            assertTrue("SARD_SELF_CHECK_INTERVAL" in message && "\"$value\"" in message, message)
        }
    }
}
