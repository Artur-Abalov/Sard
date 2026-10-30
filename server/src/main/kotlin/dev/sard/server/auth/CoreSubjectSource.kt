// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.auth

import dev.sard.server.api.SESSION_REQUEST_ATTRIBUTE
import dev.sard.server.extension.SUBJECT_REQUEST_ATTRIBUTE
import dev.sard.server.extension.Subject
import dev.sard.server.extension.SubjectSource
import org.springframework.web.context.request.RequestContextHolder
import org.springframework.web.context.request.ServletRequestAttributes

/**
 * The [SubjectSource] of the core (CORE1 Р26): the subject of the core session of the request on this thread, else
 * the one a replaced session filter put under [SUBJECT_REQUEST_ATTRIBUTE]; none outside a request (the scheduler,
 * the verification cycle) and in the wizard, which runs with a setup session, not an administrator's.
 */
class CoreSubjectSource : SubjectSource {
    override fun current(): Subject? {
        val request = (RequestContextHolder.getRequestAttributes() as? ServletRequestAttributes)?.request
        return (request?.getAttribute(SESSION_REQUEST_ATTRIBUTE) as? AdminSession)?.subject
            ?: request?.getAttribute(SUBJECT_REQUEST_ATTRIBUTE) as? Subject
    }
}
