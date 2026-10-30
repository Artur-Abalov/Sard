// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.sample

import dev.sard.server.extension.Subject
import dev.sard.server.extension.SubjectSource
import org.springframework.web.context.request.RequestContextHolder
import org.springframework.web.context.request.ServletRequestAttributes

/**
 * `sard.sample.subject = "<kind>:<id>"`: every request reports this subject, work of the server outside a request
 * reports none. It only names the subject; the session of the core still decides whether the request may come in.
 */
class SampleSubjectSource(
    private val subject: Subject,
) : SubjectSource {
    override fun current(): Subject? {
        val inRequest = RequestContextHolder.getRequestAttributes() is ServletRequestAttributes
        return subject.takeIf { inRequest }
    }

    companion object {
        /** "sso-user:alice" is kind "sso-user", id "alice". */
        fun parse(text: String): Subject {
            require(":" in text) { "sard.sample.subject must be <kind>:<id>, got '$text'" }
            return Subject(text.substringBefore(":"), text.substringAfter(":"))
        }
    }
}
