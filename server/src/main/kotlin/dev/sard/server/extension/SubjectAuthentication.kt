// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.extension

import org.springframework.security.authentication.AbstractAuthenticationToken

/**
 * The authentication of a signed-in user that carries the [Subject] it stands for (CORE1 Р39). The session of the
 * core yields `SubjectAuthentication(Subject.ADMINISTRATOR)`; the filter an extension adds through
 * [SardSecurityCustomizer] puts one into the security context of the request to let its user in. The core's
 * [SubjectSource] reads it from the context of the current request. [authenticated] false makes a token the core
 * refuses with 401, as any authentication that is not authenticated.
 */
class SubjectAuthentication
    @JvmOverloads
    constructor(
        val subject: Subject,
        authenticated: Boolean = true,
    ) : AbstractAuthenticationToken(emptyList()) {
        init {
            isAuthenticated = authenticated
        }

        override fun getCredentials(): Any? = null

        override fun getPrincipal(): Any = subject

        override fun getName(): String = subject.id
    }
