// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.extension

/**
 * The one seam through which the core learns who is acting (CORE1 Р26): the access policy is asked about this
 * subject and the `actor` of every event is this subject. It only names the subject and never authenticates: a
 * request without a core session (or a subject put under [SUBJECT_REQUEST_ATTRIBUTE] by a replaced session filter)
 * is refused with 401 whatever it answers (Р30). `null` means nobody (the scheduler, the verification cycle, the
 * first-run wizard). A source that throws refuses the operation the policy is asked about (403 access_denied) and
 * leaves the `actor` of an event null (Р31).
 *
 * The core's implementation answers with the subject of the core session, else the one under
 * [SUBJECT_REQUEST_ATTRIBUTE]. An extension replaces it with an auto-configuration ordered
 * `before = [SubjectSourceAutoConfiguration::class]`; two replacements from different extensions refuse the start.
 */
fun interface SubjectSource {
    fun current(): Subject?
}
