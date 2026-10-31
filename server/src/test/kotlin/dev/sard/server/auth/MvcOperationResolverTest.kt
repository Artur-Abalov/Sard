// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.auth

import dev.sard.server.extension.Operation
import dev.sard.testext.security.ThingsStub
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import org.springframework.beans.factory.ObjectProvider
import org.springframework.beans.factory.support.StaticListableBeanFactory
import org.springframework.mock.web.MockHttpServletRequest
import org.springframework.mock.web.MockServletContext
import org.springframework.web.context.support.StaticWebApplicationContext
import org.springframework.web.servlet.HandlerMapping
import org.springframework.web.servlet.mvc.method.annotation.RequestMappingHandlerMapping
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNull

/** Rule "Политика доступа спрашивается в цепочке один раз и только об операциях" (Р40): the template of the handler. */
@MutFlowTest
class MvcOperationResolverTest {
    private val mapping =
        RequestMappingHandlerMapping().apply {
            val context = StaticWebApplicationContext().apply { servletContext = MockServletContext() }
            context.registerSingleton("things", ThingsStub::class.java)
            context.refresh()
            setApplicationContext(context)
            afterPropertiesSet()
        }
    private val provider: ObjectProvider<RequestMappingHandlerMapping> =
        StaticListableBeanFactory(mapOf("requestMappingHandlerMapping" to mapping))
            .getBeanProvider(RequestMappingHandlerMapping::class.java)
    private val resolver = MvcOperationResolver(provider)

    private fun operationOf(request: MockHttpServletRequest): Operation? {
        val operation = MutFlow.underTest { resolver.operationOf(request) }
        return operation
    }

    @Test
    fun `the operation is the method and the path template of the handler`() {
        val request = MockHttpServletRequest("GET", "/api/v1/things/0192f7a0-0000-7000-8000-000000000999")

        assertEquals(Operation("GET", "/api/v1/things/{thingId}"), operationOf(request))
    }

    @Test
    fun `a request without a handler, with a method without one or with another media type has no operation`() {
        assertNull(operationOf(MockHttpServletRequest("GET", "/api/v1/no-such-resource")))
        assertNull(operationOf(MockHttpServletRequest("PATCH", "/api/v1/things")))
        val text = MockHttpServletRequest("POST", "/api/v1/things").apply { contentType = "text/plain" }
        assertNull(operationOf(text))
    }

    @Test
    fun `the lookup leaves no attribute on the request`() {
        val request = MockHttpServletRequest("GET", "/api/v1/things")

        operationOf(request)

        assertNull(request.getAttribute(HandlerMapping.BEST_MATCHING_PATTERN_ATTRIBUTE))
        assertEquals(emptyList(), request.attributeNames.toList())
    }
}
