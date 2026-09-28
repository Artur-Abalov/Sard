// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.auth

import dev.sard.server.ClockAutoConfiguration
import dev.sard.server.api.SessionApi
import dev.sard.server.api.SignInResult
import dev.sard.server.extension.TenancyAutoConfiguration
import org.springframework.beans.factory.NoSuchBeanDefinitionException
import org.springframework.boot.autoconfigure.AutoConfiguration
import org.springframework.boot.autoconfigure.AutoConfigurations
import org.springframework.boot.test.context.runner.ApplicationContextRunner
import org.springframework.context.annotation.Bean
import tools.jackson.databind.ObjectMapper
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertIs
import kotlin.test.assertTrue

private const val PASSWORD = "correct-horse-battery"

/** Stands in for an enterprise sign-in starter (SSO, say); ordered before the open core's default. */
@AutoConfiguration(before = [AdminAuthAutoConfiguration::class])
class FakeSsoStarter {
    @Bean
    fun sessionApi(): SessionApi =
        object : SessionApi {
            override fun createSession(
                password: String,
                clientAddress: String,
                previousSessionId: String?,
            ) = SignInResult.SignedIn("fake-sso-session")

            override fun getSession(sessionId: String) = throw NotImplementedError()

            override fun deleteSession(
                sessionId: String,
                clientAddress: String,
            ) = Unit
        }
}

class AdminAuthAutoConfigurationTest {
    private val runner =
        ApplicationContextRunner()
            .withConfiguration(
                AutoConfigurations.of(
                    ClockAutoConfiguration::class.java,
                    TenancyAutoConfiguration::class.java,
                    AdminAuthAutoConfiguration::class.java,
                ),
            ).withBean(ObjectMapper::class.java, { ObjectMapper() })

    @Test
    fun `the open core provides password sign-in`() {
        runner.withPropertyValues("SARD_ADMIN_PASSWORD=$PASSWORD").run { ctx ->
            val api = ctx.getBean(SessionApi::class.java)
            assertIs<SessionApiImpl>(api)
            assertIs<SignInResult.SignedIn>(api.createSession(PASSWORD, "203.0.113.10", null))
        }
    }

    @Test
    fun `an enterprise starter replaces SessionApi and needs no SARD_ADMIN_PASSWORD`() {
        runner
            .withConfiguration(AutoConfigurations.of(FakeSsoStarter::class.java))
            .run { ctx ->
                assertTrue(ctx.startupFailure == null, "${ctx.startupFailure}")
                assertEquals(1, ctx.getBeanNamesForType(SessionApi::class.java).size)
                val api = ctx.getBean(SessionApi::class.java)
                val result = api.createSession("anything", "203.0.113.10", null)
                assertEquals(SignInResult.SignedIn("fake-sso-session"), result)
                assertFailsWith<NoSuchBeanDefinitionException> { ctx.getBean(AdminPasswordAuthenticator::class.java) }
            }
    }
}
