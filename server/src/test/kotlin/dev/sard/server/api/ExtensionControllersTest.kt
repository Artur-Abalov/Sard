// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import dev.sard.server.extension.ExtensionRegistry
import dev.sard.server.x.PkgInsideController
import dev.sard.server.x.PkgOutsideController
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import org.springframework.web.method.HandlerMethod
import org.springframework.web.servlet.mvc.condition.PatternsRequestCondition
import org.springframework.web.servlet.mvc.method.RequestMappingInfo
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

/** Rule "Эндпоинты расширения подчинены сессии ядра" (Р8, Р27): which handlers are checked and which refuse. */
@MutFlowTest
class ExtensionControllersTest {
    private val core = ExtensionControllers::class.java.protectionDomain.codeSource.location

    private fun handler(
        path: String,
        type: Class<*> = PkgOutsideController::class.java,
    ): Pair<RequestMappingInfo, HandlerMethod> =
        RequestMappingInfo.paths(path).build() to HandlerMethod(type.getDeclaredConstructor().newInstance(), "ping")

    private fun refused(vararg handlers: Pair<RequestMappingInfo, HandlerMethod>) =
        MutFlow.underTest { ExtensionControllers(core).outsideApi(handlers.toList()) }

    @Test
    fun `a controller outside api v1 in a package of the core namespace is refused when it is not from the core`() {
        val refused = refused(handler("/ext/ping"))

        assertEquals(listOf("/ext/ping (dev.sard.server.x.PkgOutsideController)"), refused)
    }

    @Test
    fun `a controller under api v1 passes wherever it comes from`() {
        assertEquals(emptyList(), refused(handler("/api/v1/pkg-inside/ping", PkgInsideController::class.java)))
    }

    @Test
    fun `a controller of the core outside api v1 passes`() {
        val actuatorLike = RequestMappingInfo.paths("/actuator/health").build() to coreHandler()

        assertEquals(emptyList(), refused(actuatorLike))
    }

    @Test
    fun `a controller of a framework outside api v1 passes`() {
        val docs =
            RequestMappingInfo.paths("/v3/api-docs").build() to
                HandlerMethod(
                    org.springframework.core.io
                        .ClassPathResource("x"),
                    "getPath",
                )

        assertEquals(emptyList(), refused(docs))
    }

    @Test
    fun `a handler whose path cannot be determined counts as outside api v1`() {
        @Suppress("DEPRECATION")
        val info =
            RequestMappingInfo(PatternsRequestCondition("/api/v1/ext/ping"), null, null, null, null, null, null, null)
        val noPath = info to HandlerMethod(PkgOutsideController(), "ping")

        val refused = refused(noPath)

        assertEquals(listOf("<no path> (dev.sard.server.x.PkgOutsideController)"), refused)
        assertTrue(info.pathPatternsCondition == null)
    }

    @Test
    fun `without a known location of the core nothing is the core's`() {
        val core = RequestMappingInfo.paths("/actuator/health").build() to coreHandler()
        val bootstrap = RequestMappingInfo.paths("/jdk").build() to HandlerMethod(ArrayList<String>(), "size")

        val refused = MutFlow.underTest { ExtensionControllers(null).outsideApi(listOf(core, bootstrap)) }

        val registry = "/actuator/health (dev.sard.server.extension.ExtensionRegistry)"
        assertEquals(listOf(registry, "/jdk (java.util.ArrayList)"), refused)
    }

    @Test
    fun `a class without a code source is not the core's`() {
        val bootstrap = RequestMappingInfo.paths("/jdk").build() to HandlerMethod(ArrayList<String>(), "size")

        assertEquals(listOf("/jdk (java.util.ArrayList)"), refused(bootstrap))
    }

    private fun coreHandler() = HandlerMethod(ExtensionRegistry(emptyList()), "ids")
}
