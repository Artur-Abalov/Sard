// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.extension

/** Who a session stands for (CORE1 Р4, Р17). The sessions of the open core are all [ADMINISTRATOR]. */
data class Subject(
    val kind: String,
    val id: String,
) {
    companion object {
        /** The one administrator of the open core. */
        val ADMINISTRATOR = Subject("administrator", "admin")
    }
}

/** An operation of the REST API as OpenAPI names it: the HTTP method and the path template (Р17). */
data class Operation(
    val method: String,
    val pathTemplate: String,
) {
    override fun toString() = "$method $pathTemplate"
}

/**
 * The seam for access (CORE1 Р4, Р15-Р17). The core asks it, in its security chain, after the Origin check (403
 * origin_rejected) and the authentication (401) for every operation but the public ones, the wizard's and sign-out;
 * a refusal is 403 access_denied, and a policy that throws refuses too. Work of the server itself (the scheduler,
 * the verification cycle) is not asked.
 */
fun interface AccessPolicy {
    fun permits(
        subject: Subject,
        operation: Operation,
    ): Boolean
}
