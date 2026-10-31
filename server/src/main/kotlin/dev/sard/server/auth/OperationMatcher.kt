// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.auth

import dev.sard.server.extension.Operation
import jakarta.servlet.http.HttpServletRequest
import org.springframework.http.server.PathContainer
import org.springframework.security.web.util.matcher.RequestMatcher
import org.springframework.web.util.pattern.PathPattern
import org.springframework.web.util.pattern.PathPatternParser

private val PARSER =
    PathPatternParser().apply {
        isCaseSensitive = true
    }

/**
 * Matches the operations by the method and the exact path of the request, as the chain has always told them apart
 * (CORE1 Р35): no trailing slash, case matters, HEAD is not GET. The path templates of a public operation of an
 * extension may carry variables (`/api/v1/things/{id}`).
 */
class OperationMatcher(
    operations: Collection<Operation>,
) : RequestMatcher {
    private val patterns: List<Pair<String, PathPattern>> =
        operations.map { it.method to PARSER.parse(it.pathTemplate) }

    constructor(operations: Set<String>) : this(
        operations.map { text -> text.substringBefore(' ').let { Operation(it, text.substringAfter(' ')) } },
    )

    override fun matches(request: HttpServletRequest): Boolean {
        val path = PathContainer.parsePath(request.requestURI)
        return patterns.any { (method, pattern) -> method == request.method && pattern.matches(path) }
    }
}
