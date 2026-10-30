// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.extension

import dev.sard.testext.outside.InsideController
import dev.sard.testext.outside.OutsideController
import org.springframework.boot.autoconfigure.AutoConfigurations
import org.springframework.boot.test.context.TestConfiguration
import org.springframework.boot.test.context.runner.WebApplicationContextRunner
import org.springframework.web.servlet.config.annotation.EnableWebMvc
import kotlin.test.Test
import kotlin.test.assertNotNull
import kotlin.test.assertNull
import kotlin.test.assertTrue

@TestConfiguration(proxyBeanMethods = false)
@EnableWebMvc
class MvcForOutside

/** Rule "Эндпоинты расширения ... лежат под api v1" (server-core-extensions.feature, @startup). */
class OutsideControllerTest {
    private val runner =
        WebApplicationContextRunner()
            .withUserConfiguration(MvcForOutside::class.java)
            .withConfiguration(AutoConfigurations.of(ExtensionWebAutoConfiguration::class.java))

    @Test
    fun `Контроллер расширения вне api v1 останавливает старт`() {
        runner.withBean(OutsideController::class.java).run { ctx ->
            assertNotNull(ctx.startupFailure)
            val text = generateSequence(ctx.startupFailure) { it.cause }.map { it.message }.joinToString()
            assertTrue("outside" in text && "/ext/ping" in text, text)
        }
    }

    @Test
    fun `Контроллер расширения под api v1 стартует`() {
        runner.withBean(InsideController::class.java).run { ctx -> assertNull(ctx.startupFailure) }
    }
}
