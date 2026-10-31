// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.extension

import org.springframework.security.config.annotation.web.builders.HttpSecurity

/**
 * The seam through which an extension (SSO, OIDC, LDAP) adds its sign-in to the one security chain of the core
 * (CORE1 Р41). The core applies every customizer to its chain after its own access rules, in the order of
 * `@Order`, then of the id of the extension; the type [HttpSecurity] is part of the frozen ABI.
 *
 * - [publicOperations] are the operations of the extension that are open without a session, normally the entry
 *   points of its sign-in. They must be under `/api/v1`, or the start is refused; the access policy is not asked about
 *   them and OpenAPI gives them an empty security requirement.
 * - [customize] adds filters (a filter that puts a [SubjectAuthentication] into the security context, or
 *   `oauth2Login`). It must not set access rules for paths: the chain would fail to assemble and the start is
 *   refused (the failure is closed). An exception it throws refuses the start too.
 *
 * An extension declares a bean of this type from its auto-configuration, named after its id. A
 * `SecurityFilterChain` or a `WebSecurityCustomizer` of an extension refuses the start.
 */
interface SardSecurityCustomizer {
    /** The operations open without a session; each is under `/api/v1`. */
    val publicOperations: Set<Operation> get() = emptySet()

    /** Adds the filters of the sign-in to the chain of the core. */
    fun customize(http: HttpSecurity)
}
