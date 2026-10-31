// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.auth

import dev.sard.server.extension.Subject
import dev.sard.server.extension.SubjectAuthentication
import dev.sard.server.extension.SubjectSource
import org.springframework.security.core.context.SecurityContextHolder

/**
 * The [SubjectSource] of the core (CORE1 Р26, Р39): the subject of the [SubjectAuthentication] in the security
 * context of the request on this thread; none outside a request (the scheduler, the verification cycle: the context
 * lives for one request only) and in the wizard, which runs with a setup session, not an administrator's.
 */
class CoreSubjectSource : SubjectSource {
    override fun current(): Subject? {
        val authentication = SecurityContextHolder.getContext().authentication as? SubjectAuthentication
        return authentication?.takeIf { it.isAuthenticated }?.subject
    }
}
