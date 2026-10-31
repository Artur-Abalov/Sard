// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.extension

import org.springframework.boot.autoconfigure.AutoConfiguration
import org.springframework.boot.autoconfigure.AutoConfigurations
import org.springframework.boot.test.context.TestConfiguration
import org.springframework.boot.test.context.runner.ApplicationContextRunner
import org.springframework.context.annotation.Bean
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNotNull
import kotlin.test.assertTrue

@AutoConfiguration(before = [AccessPolicyAutoConfiguration::class])
class FakePolicyStarter {
    @Bean
    fun policy(): AccessPolicy = AccessPolicy { _, _ -> false }
}

@TestConfiguration(proxyBeanMethods = false)
class TwoPolicyExtensions {
    @Bean("pol-a-policy")
    fun a(): AccessPolicy = AccessPolicy { _, _ -> true }

    @Bean("pol-b-policy")
    fun b(): AccessPolicy = AccessPolicy { _, _ -> true }
}

class AccessPolicyAutoConfigurationTest {
    private val runner =
        ApplicationContextRunner().withConfiguration(AutoConfigurations.of(AccessPolicyAutoConfiguration::class.java))
    private val delete = Operation("DELETE", "/api/v1/sources/{sourceId}")

    @Test
    fun `Политика ядра разрешает администратору всё`() {
        runner.run { ctx ->
            assertTrue(ctx.getBean(AccessPolicy::class.java).permits(Subject.ADMINISTRATOR, delete))
        }
    }

    @Test
    fun `Расширение заменяет политику доступа`() {
        runner.withConfiguration(AutoConfigurations.of(FakePolicyStarter::class.java)).run { ctx ->
            assertFalse(ctx.getBean(AccessPolicy::class.java).permits(Subject.ADMINISTRATOR, delete))
        }
    }

    @Test
    fun `Субъект сессии ядра - administrator с id admin а операция - метод и шаблон пути`() {
        assertEquals(Subject("administrator", "admin"), Subject.ADMINISTRATOR)
        assertEquals("DELETE /api/v1/sources/{sourceId}", delete.toString())
    }

    @Test
    fun `Две политики доступа от разных расширений останавливают старт`() {
        runner.withUserConfiguration(TwoPolicyExtensions::class.java).run { ctx ->
            assertNotNull(ctx.startupFailure)
            val text = generateSequence(ctx.startupFailure) { it.cause }.map { it.message }.joinToString()
            assertTrue("pol-a" in text && "pol-b" in text, text)
        }
    }
}
