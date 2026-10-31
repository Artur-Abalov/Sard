// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.auth

import dev.sard.server.ClockAutoConfiguration
import dev.sard.server.api.PasswordChangeResult
import dev.sard.server.api.SessionApi
import dev.sard.server.api.SignInResult
import dev.sard.server.events.EventsAutoConfiguration
import dev.sard.server.extension.AccessPolicyAutoConfiguration
import dev.sard.server.extension.SubjectSourceAutoConfiguration
import dev.sard.server.extension.TenancyAutoConfiguration
import org.springframework.beans.factory.NoSuchBeanDefinitionException
import org.springframework.boot.autoconfigure.AutoConfiguration
import org.springframework.boot.autoconfigure.AutoConfigurations
import org.springframework.boot.test.context.runner.ApplicationContextRunner
import org.springframework.context.annotation.Bean
import org.springframework.jdbc.core.JdbcTemplate
import org.springframework.jdbc.datasource.DriverManagerDataSource
import tools.jackson.databind.ObjectMapper
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertIs
import kotlin.test.assertTrue

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

private const val NO_DATABASE = "jdbc:postgresql://localhost:1/none"

class AdminAuthAutoConfigurationTest {
    private val runner =
        ApplicationContextRunner()
            .withConfiguration(
                AutoConfigurations.of(
                    ClockAutoConfiguration::class.java,
                    TenancyAutoConfiguration::class.java,
                    AccessPolicyAutoConfiguration::class.java,
                    EventsAutoConfiguration::class.java,
                    SubjectSourceAutoConfiguration::class.java,
                    CoreSubjectSourceAutoConfiguration::class.java,
                    AdminAuthAutoConfiguration::class.java,
                ),
            ).withBean(ObjectMapper::class.java, { ObjectMapper() })
            .withBean(JdbcTemplate::class.java, { JdbcTemplate(DriverManagerDataSource(NO_DATABASE)) })

    @Test
    fun `the open core provides password sign-in against the stored administrator`() {
        runner.run { ctx ->
            assertTrue(ctx.startupFailure == null, "${ctx.startupFailure}")
            assertIs<SessionApiImpl>(ctx.getBean(SessionApi::class.java))
            assertIs<StoredAdminSetup>(ctx.getBean(AdminSetup::class.java))
            assertIs<JdbcAdministrators>(ctx.getBean(Administrators::class.java))
        }
    }

    @Test
    fun `an enterprise starter replaces SessionApi, the admin step counts as done and no administrator is stored`() {
        runner
            .withConfiguration(AutoConfigurations.of(FakeSsoStarter::class.java))
            .run { ctx ->
                assertTrue(ctx.startupFailure == null, "${ctx.startupFailure}")
                assertEquals(1, ctx.getBeanNamesForType(SessionApi::class.java).size)
                val api = ctx.getBean(SessionApi::class.java)
                val result = api.createSession("anything", "203.0.113.10", null)
                assertEquals(SignInResult.SignedIn("fake-sso-session"), result)
                assertFailsWith<NoSuchBeanDefinitionException> { ctx.getBean(Administrators::class.java) }
                assertEquals(ExternalAdminSetup, ctx.getBean(AdminSetup::class.java))
                assertEquals(PasswordChangeResult.NotSupported, api.changePassword("s", "a", "b", "203.0.113.10"))
            }
    }
}
