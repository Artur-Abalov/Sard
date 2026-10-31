// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.auth

import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import org.mockito.Mockito
import org.springframework.beans.factory.support.DefaultListableBeanFactory
import org.springframework.boot.autoconfigure.AutoConfigurationImportSelector
import org.springframework.boot.autoconfigure.AutoConfigurationMetadata
import org.springframework.boot.autoconfigure.EnableAutoConfiguration
import org.springframework.context.annotation.Configuration
import org.springframework.core.io.DefaultResourceLoader
import org.springframework.core.type.AnnotationMetadata
import org.springframework.mock.env.MockEnvironment
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

private const val DEFAULT_USER = "org.springframework.boot.security.autoconfigure.UserDetailsServiceAutoConfiguration"
private const val GRPC = "org.springframework.boot.grpc.server.autoconfigure.security"
private const val JACKSON = "org.springframework.boot.jackson.autoconfigure.JacksonAutoConfiguration"

@Configuration(proxyBeanMethods = false)
@EnableAutoConfiguration
class AutoConfigured

/** Rules "Цепочка ядра не создаёт ... cookie, кроме своих" (Р34) and "gRPC вне Spring Security" (Р45). */
@MutFlowTest
class SecurityAutoConfigurationFilterTest {
    private val metadata = Mockito.mock(AutoConfigurationMetadata::class.java)

    private fun match(vararg classes: String): List<Boolean> =
        MutFlow.underTest { SecurityAutoConfigurationFilter().match(arrayOf(*classes), metadata) }.toList()

    @Test
    fun `the default user is not imported, the rest is`() {
        assertEquals(listOf(false, true), match(DEFAULT_USER, "x.Y"))
    }

    @Test
    fun `the security of gRPC is not imported`() {
        assertEquals(
            listOf(false, false),
            match("$GRPC.GrpcServerSecurityAutoConfiguration", "$GRPC.GrpcServerOAuth2ResourceServerAutoConfiguration"),
        )
    }

    @Test
    fun `an exclusion of the operator in the list form is still honoured`() {
        val selector = AutoConfigurationImportSelector()
        selector.setBeanFactory(DefaultListableBeanFactory())
        selector.setResourceLoader(DefaultResourceLoader())
        selector.setBeanClassLoader(javaClass.classLoader)
        selector.setEnvironment(MockEnvironment().withProperty("spring.autoconfigure.exclude[0]", JACKSON))

        val imported = selector.selectImports(AnnotationMetadata.introspect(AutoConfigured::class.java)).toList()

        assertTrue(imported.isNotEmpty())
        assertTrue(JACKSON !in imported && DEFAULT_USER !in imported, "$imported")
        assertTrue(imported.any { it.endsWith("TransactionAutoConfiguration") }, "$imported")
    }
}
