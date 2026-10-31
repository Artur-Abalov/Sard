// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.auth

import dev.sard.server.extension.AccessPolicyAutoConfiguration
import dev.sard.server.extension.Operation
import dev.sard.server.extension.SardSecurityCustomizer
import dev.sard.server.extension.SubjectSourceAutoConfiguration
import org.springframework.beans.factory.annotation.AnnotatedBeanDefinition
import org.springframework.boot.autoconfigure.AutoConfigurations
import org.springframework.boot.security.autoconfigure.SecurityAutoConfiguration
import org.springframework.boot.security.autoconfigure.web.servlet.ServletWebSecurityAutoConfiguration
import org.springframework.boot.test.context.TestConfiguration
import org.springframework.boot.test.context.assertj.AssertableWebApplicationContext
import org.springframework.boot.test.context.runner.WebApplicationContextRunner
import org.springframework.context.annotation.Bean
import org.springframework.security.config.annotation.web.builders.HttpSecurity
import org.springframework.security.config.annotation.web.builders.WebSecurity
import org.springframework.security.config.annotation.web.configuration.WebSecurityCustomizer
import org.springframework.security.web.SecurityFilterChain
import org.springframework.web.servlet.config.annotation.EnableWebMvc
import tools.jackson.databind.ObjectMapper
import java.time.Clock
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNotNull
import kotlin.test.assertNull
import kotlin.test.assertTrue

@TestConfiguration(proxyBeanMethods = false)
@EnableWebMvc
class SecurityBasics {
    @Bean
    fun clock(): Clock = Clock.systemUTC()

    @Bean
    fun sessionStore(clock: Clock) = SessionStore(clock)

    @Bean
    fun objectMapper() = ObjectMapper()
}

/** An extension that declares a chain of its own instead of a customizer. */
@TestConfiguration(proxyBeanMethods = false)
class OwnChainExtension {
    @Bean("own-chain")
    fun ownChain(http: HttpSecurity): SecurityFilterChain = http.build()
}

/** An extension that leaves paths out of the chain. */
@TestConfiguration(proxyBeanMethods = false)
class IgnoringExtension {
    @Bean("ignoring")
    fun ignoring(): WebSecurityCustomizer {
        val bypass = { web: WebSecurity -> web.ignoring().requestMatchers("/api/v1/**").let { } }
        return WebSecurityCustomizer(bypass)
    }
}

private fun customizer(
    operations: Set<Operation> = emptySet(),
    customize: (HttpSecurity) -> Unit = {},
) = object : SardSecurityCustomizer {
    override val publicOperations = operations

    override fun customize(http: HttpSecurity) = customize(http)
}

/**
 * Rule "Расширение не может обойти цепочку ядра, и это видно при старте" (spring-security.feature, @startup, Р44):
 * the chain is assembled in a context with the core's security configuration and the extension under test.
 */
class SecurityStartupChecksTest {
    private val runner =
        WebApplicationContextRunner()
            .withUserConfiguration(SecurityBasics::class.java)
            .withConfiguration(
                AutoConfigurations.of(
                    SecurityAutoConfiguration::class.java,
                    ServletWebSecurityAutoConfiguration::class.java,
                    AccessPolicyAutoConfiguration::class.java,
                    SubjectSourceAutoConfiguration::class.java,
                    CoreSubjectSourceAutoConfiguration::class.java,
                    CoreSecurityAutoConfiguration::class.java,
                ),
            )

    private fun failureOf(context: AssertableWebApplicationContext): String {
        assertNotNull(context.startupFailure, "the server started")
        return generateSequence(context.startupFailure) { it.cause }.map { it.message }.joinToString(" | ")
    }

    @Test
    fun `Своя цепочка безопасности расширения останавливает старт`() {
        runner.withUserConfiguration(OwnChainExtension::class.java).run { ctx ->
            val text = failureOf(ctx)
            assertTrue("own-chain" in text && "SardSecurityCustomizer" in text, text)
        }
    }

    @Test
    fun `Исключение путей из цепочки расширением останавливает старт`() {
        runner.withUserConfiguration(IgnoringExtension::class.java).run { ctx ->
            val text = failureOf(ctx)
            assertTrue("ignoring" in text && "SardSecurityCustomizer" in text, text)
        }
    }

    @Test
    fun `Публичная операция расширения вне api v1 останавливает старт`() {
        val outside = customizer(setOf(Operation("GET", "/ext/open")))

        runner.withBean("open-outside", SardSecurityCustomizer::class.java, { outside }).run { ctx ->
            val text = failureOf(ctx)
            assertTrue("open-outside" in text && "GET /ext/open" in text, text)
        }
    }

    @Test
    fun `Настройщик, который задаёт правила доступа к путям, останавливает старт`() {
        val permitAll =
            customizer { http -> http.authorizeHttpRequests { it.requestMatchers("/api/v1/sources").permitAll() } }

        runner.withBean("permit-all", SardSecurityCustomizer::class.java, { permitAll }).run { ctx ->
            val text = failureOf(ctx)
            assertTrue("permit-all" in text, text)
        }
    }

    @Test
    fun `Настройщик, который сужает цепочку ядра, останавливает старт`() {
        val narrowing = customizer { http -> http.securityMatcher("/api/v1/sso/**") }

        runner.withBean("narrow-sec", SardSecurityCustomizer::class.java, { narrowing }).run { ctx ->
            val text = failureOf(ctx)
            assertTrue("narrow-sec" in text && "SardSecurityCustomizer" in text, text)
        }
    }

    @Test
    fun `Сбой настройщика расширения останавливает старт`() {
        val broken = customizer { error("customizer broken") }

        runner.withBean("broken-sec", SardSecurityCustomizer::class.java, { broken }).run { ctx ->
            val text = failureOf(ctx)
            assertTrue("broken-sec" in text && "customizer broken" in text, text)
        }
    }

    @Test
    fun `Настройщик с публичной операцией под api v1 стартует`() {
        val open = customizer(setOf(Operation("GET", "/api/v1/open-ext/start")))

        runner.withBean("open-ext", SardSecurityCustomizer::class.java, { open }).run { ctx ->
            assertNull(ctx.startupFailure)
        }
    }

    @Test
    fun `Ядро без расширений стартует со своей цепочкой, и она одна`() {
        runner.run { ctx ->
            assertNull(ctx.startupFailure)
            val chains = ctx.getBeansOfType(SecurityFilterChain::class.java)
            assertEquals(listOf("sardSecurityFilterChain"), chains.keys.toList())
            val definition = ctx.beanFactory.getBeanDefinition("sardSecurityFilterChain") as AnnotatedBeanDefinition
            val declaring = definition.factoryMethodMetadata?.declaringClassName
            assertEquals(CoreSecurityAutoConfiguration::class.java.name, declaring)
        }
    }

    @Test
    fun `Настройщики применяются по порядку Order, затем по имени бина`() {
        val applied = mutableListOf<String>()
        val named = { name: String -> customizer { applied += name } }

        runner
            .withBean("b-second", SardSecurityCustomizer::class.java, { named("b-second") })
            .withBean("a-first", SardSecurityCustomizer::class.java, { named("a-first") })
            .run { ctx ->
                assertNull(ctx.startupFailure)
                assertEquals(listOf("a-first", "b-second"), applied)
            }
    }
}
