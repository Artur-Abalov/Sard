// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.auth

import dev.sard.server.extension.SardSecurityCustomizer
import jakarta.servlet.Filter
import org.springframework.security.web.DefaultSecurityFilterChain
import org.springframework.security.web.SecurityFilterChain
import org.springframework.security.web.util.matcher.AnyRequestMatcher

/**
 * After the customizers have been applied the chain must still be the whole chain of the core: one that matches every
 * request and still has the headers and the Origin check. A customizer that narrows it (`securityMatcher`) or takes
 * these filters out would leave /api/v1 outside the chain, so the start is refused and the text names them (Р44).
 */
internal fun checkChainIntact(
    chain: SecurityFilterChain,
    customizers: List<NamedCustomizer>,
) {
    check(customizers.isEmpty() || isWhole(chain)) {
        val named = customizers.joinToString { describe(it.name, it.origin) }
        "The security chain of the core is no longer the whole chain after the customizers $named: one of them " +
            "narrowed it or took out the headers or the Origin check. An extension adds filters with a " +
            "${SardSecurityCustomizer::class.java.simpleName}; it does not change what the chain covers."
    }
}

private val REQUIRED_FILTERS = listOf(ApiHeadersFilter::class.java, OriginGuardFilter::class.java)

private fun isWhole(chain: SecurityFilterChain): Boolean {
    val whole = chain as? DefaultSecurityFilterChain ?: return false
    return covers(whole) && hasRequiredFilters(whole.filters)
}

private fun covers(chain: DefaultSecurityFilterChain): Boolean = chain.requestMatcher is AnyRequestMatcher

private fun hasRequiredFilters(filters: List<Filter>): Boolean {
    val present = { type: Class<*> -> filters.any(type::isInstance) }
    return REQUIRED_FILTERS.all(present)
}
