// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.auth

import dev.sard.server.extension.Operation
import jakarta.servlet.http.HttpServletRequest
import jakarta.servlet.http.HttpServletRequestWrapper
import org.springframework.beans.factory.ObjectProvider
import org.springframework.web.servlet.HandlerMapping
import org.springframework.web.servlet.mvc.method.annotation.RequestMappingHandlerMapping
import org.springframework.web.util.ServletRequestPathUtils
import java.util.concurrent.ConcurrentHashMap

/**
 * The operation of a request by the path template of the handler Spring MVC would choose for it (CORE1 Р40), which
 * is known only after the handler mapping has looked at the request, and the chain is asked before the servlet. The
 * lookup runs against a wrapper that keeps the attributes the mapping sets to itself, so the request the servlet then
 * handles is left as it was. No handler, or a method or media type without one (404, 405, 415), is no operation.
 * The mappings come through a provider: they must not be created while the security configuration is built.
 */
class MvcOperationResolver(
    private val mappings: ObjectProvider<RequestMappingHandlerMapping>,
) : OperationResolver {
    override fun operationOf(request: HttpServletRequest): Operation? {
        val lookup = IsolatedAttributes(request)
        ServletRequestPathUtils.parseAndCache(lookup)
        val matched = mappings.orderedStream().anyMatch { handles(it, lookup) }
        val template = lookup.getAttribute(HandlerMapping.BEST_MATCHING_PATTERN_ATTRIBUTE) as? String
        return template?.takeIf { matched }?.let { Operation(request.method, it) }
    }

    private fun handles(
        mapping: RequestMappingHandlerMapping,
        request: HttpServletRequest,
    ): Boolean = runCatching { mapping.getHandler(request) }.getOrNull() != null

    /** Attributes set by the lookup stay in the wrapper; the request's own are readable through it. */
    private class IsolatedAttributes(
        request: HttpServletRequest,
    ) : HttpServletRequestWrapper(request) {
        private val own = ConcurrentHashMap<String, Any>()

        override fun setAttribute(
            name: String,
            value: Any?,
        ) {
            if (value == null) own.remove(name) else own[name] = value
        }

        override fun getAttribute(name: String): Any? = own[name] ?: super.getAttribute(name)

        override fun removeAttribute(name: String) {
            own.remove(name)
        }
    }
}
